package sethandler

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nokia/CPU-Pooler/pkg/checkpoint"
	"github.com/nokia/CPU-Pooler/pkg/k8sclient"
	"github.com/nokia/CPU-Pooler/pkg/types"
	"github.com/nokia/CPU-Pooler/pkg/utils"
	"github.com/nxsre/toolkit/pkg/topology"
	"golang.org/x/sys/unix"
	"io"
	"k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/temp"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

const (
	//MaxRetryCount controls how many times we re-try a remote API operation
	MaxRetryCount = 5
	//RetryInterval controls how much time (in milliseconds) we wait between two retry attempts when talking to a remote API
	RetryInterval = 200
)

var (
	resourceBaseName       = "bonc.k8s.io"
	processConfigKey       = resourceBaseName + "/cpus"
	setterAnnotationSuffix = "cpusets-configured"
	setterAnnotationKey    = resourceBaseName + "/" + setterAnnotationSuffix
	containerPrefixList    = []string{"docker://", "containerd://"}

	// 物理机可用 CPU 总核心数，计算超售 CPU 绑定物理核心依赖次参数
	numOfCpus = int(utils.GetCpuInfo().Cores)
)

type workItem struct {
	oldPod *v1.Pod
	newPod *v1.Pod
}

// SetHandler is the data set encapsulating the configuration data needed for the CPUSetter Controller to be able to adjust cpusets
type SetHandler struct {
	poolConfig      types.PoolConfig
	cpusetRoot      string
	cpuRoot         string
	k8sClient       kubernetes.Interface
	informerFactory informers.SharedInformerFactory
	podSynced       cache.InformerSynced
	workQueue       workqueue.Interface
	stopChan        *chan struct{}
}

// SetHandler returns the SetHandler data set
func (setHandler SetHandler) SetHandler() SetHandler {
	return setHandler
}

// SetSetHandler a setter for SetHandler
func (setHandler *SetHandler) SetSetHandler(poolconf types.PoolConfig, cpusetRoot string, k8sClient kubernetes.Interface) {
	setHandler.poolConfig = poolconf
	setHandler.cpusetRoot = cpusetRoot
	setHandler.k8sClient = k8sClient
	setHandler.workQueue = workqueue.New()
}

// New creates a new SetHandler object
// Can return error if in-cluster K8s API server client could not be initialized
func New(kubeConf string, poolConfig types.PoolConfig, cpusetRoot, cpuRoot string) (*SetHandler, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeConf)
	if err != nil {
		return nil, err
	}
	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	kubeInformerFactory := informers.NewSharedInformerFactory(kubeClient, time.Second)
	podInformer := kubeInformerFactory.Core().V1().Pods().Informer()

	setHandler := SetHandler{
		poolConfig:      poolConfig,
		cpusetRoot:      cpusetRoot,
		cpuRoot:         cpuRoot,
		k8sClient:       kubeClient,
		informerFactory: kubeInformerFactory,
		podSynced:       podInformer.HasSynced,
		workQueue:       workqueue.New(),
	}
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			//setHandler.PodAdded((reflect.ValueOf(obj).Interface().(*v1.Pod)))
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			setHandler.PodChanged(reflect.ValueOf(oldObj).Interface().(*v1.Pod), reflect.ValueOf(newObj).Interface().(*v1.Pod))
		},
	})
	podInformer.SetWatchErrorHandler(setHandler.WatchErrorHandler)
	return &setHandler, nil
}

// Run kicks the CPUSetter controller into motion, synchs it with the API server, and starts the desired number of asynch worker threads to handle the Pod API events
func (setHandler *SetHandler) Run(threadiness int, stopCh *chan struct{}) error {
	setHandler.stopChan = stopCh
	setHandler.informerFactory.Start(*stopCh)
	log.Println("INFO: Starting cpusetter Controller...")
	log.Println("INFO: Waiting for Pod Controller cache to sync...")
	if ok := cache.WaitForCacheSync(*stopCh, setHandler.podSynced); !ok {
		return errors.New("failed to sync Pod Controller from cache! Are you sure everything is properly connected?")
	}
	log.Println("INFO: Starting " + strconv.Itoa(threadiness) + " cpusetter worker threads...")
	for i := 0; i < threadiness; i++ {
		go wait.Until(setHandler.runWorker, time.Second, *stopCh)
	}
	setHandler.StartReconciliation()
	log.Println("INFO: CPUSetter is successfully initialized, worker threads are now serving requests!")
	return nil
}

// PodAdded handles ADD operations
func (setHandler *SetHandler) PodAdded(pod *v1.Pod) {
	workItem := workItem{newPod: pod}
	setHandler.workQueue.Add(workItem)
}

// PodChanged handles UPDATE operations
func (setHandler *SetHandler) PodChanged(oldPod, newPod *v1.Pod) {
	//The maze wasn't meant for you either
	item := workItem{newPod: newPod, oldPod: oldPod}
	oldRestarts, oldLastRestartDate, oldRestartsStr, oldReason := k8sclient.PodStatus(oldPod)
	newRestarts, newLastRestartDate, newRestartsStr, newReason := k8sclient.PodStatus(newPod)

	//log.Println("PodChanged", oldPod.Name, newRestarts, oldReason, newReason)

	// 状态发生变化才需要放入更新队列
	if oldPod.Status.Phase != newPod.Status.Phase && newPod.Status.Phase == v1.PodRunning {
		log.Printf("oldPod ----> newPod namespace:%s  podname:%s: %s --> %s", oldPod.Namespace, oldPod.Name, oldPod.Status.Phase, newPod.Status.Phase)
		setHandler.workQueue.Add(item)
		return
	}

	if (oldReason != newReason && v1.PodPhase(newReason) == v1.PodRunning) || (oldReason == newReason && v1.PodPhase(newReason) == v1.PodRunning && newRestarts > oldRestarts) {
		log.Printf("oldPod ---> newPod, old-name:%s, restarts:%d, time:%s, str:%s, reason:%s ---> new-name:%s, restarts:%d, time:%s, str:%s, reason:%s",
			oldPod.Name, oldRestarts, oldLastRestartDate, oldRestartsStr, oldReason,
			newPod.Name, newRestarts, newLastRestartDate, newRestartsStr, newReason)
		setHandler.workQueue.Add(item)
		return
	}
}

// WatchErrorHandler is an event handler invoked when the CPUSetter Controller's connection to the K8s API server breaks
// In case the error is terminal it initiates a graceful shutdown for the whole Controller, implicitly restarting the connection by restarting the whole container
func (setHandler *SetHandler) WatchErrorHandler(r *cache.Reflector, err error) {
	if apierrors.IsResourceExpired(err) || apierrors.IsGone(err) || err == io.EOF {
		log.Println("INFO: One of the API watchers closed gracefully, re-establishing connection")
		return
	}
	//The default K8s client retry mechanism expires after a certain amount of time, and just gives-up
	//It is better to shutdown the whole process now and freshly re-build the watchers, rather than risking becoming a permanent zombie
	log.Println("ERROR: One of the API watchers closed unexpectedly with error:" + err.Error() + " restarting CPUSetter!")
	setHandler.Stop()
	//Give some time for gracefully terminating the connections
	time.Sleep(5 * time.Second)
	os.Exit(0)
}

// Stop is invoked by the main thread to initiate graceful shutdown procedure. It shuts down the event handler queue, and relays a stop signal to the Controller
func (setHandler *SetHandler) Stop() {
	*setHandler.stopChan <- struct{}{}
	setHandler.workQueue.ShutDown()
}

// StartReconciliation starts the reactive thread of SetHandler periodically checking expected and provisioned cpusets of the node
// In case a container's observed cpuset differs from the expected (i.e. container was restarted) the thread resets it to the proper value
func (setHandler *SetHandler) StartReconciliation() {
	go setHandler.startReconciliationLoop()
	log.Println("INFO: Successfully started the periodic cpuset reconciliation thread")
}

func (setHandler *SetHandler) runWorker() {
	for setHandler.processNextWorkItem() {
	}
}

func (setHandler *SetHandler) processNextWorkItem() bool {
	obj, areWeShuttingDown := setHandler.workQueue.Get()
	if areWeShuttingDown {
		log.Println("WARNING: Received shutdown command from queue in thread:" + strconv.Itoa(unix.Gettid()))
		return false
	}
	setHandler.processItemInQueue(obj)
	return true
}

func (setHandler *SetHandler) processItemInQueue(obj interface{}) {
	defer setHandler.workQueue.Done(obj)
	var item workItem
	var ok bool
	if item, ok = obj.(workItem); !ok {
		log.Println("WARNING: Cannot decode work item from queue in thread: " + strconv.Itoa(unix.Gettid()) + ", be aware that we are skipping some events!!!")
		return
	}
	setHandler.handlePods(item)
}

func (setHandler *SetHandler) handlePods(item workItem) {
	isItMyPod, pod := shouldPodBeHandled(*item.newPod)
	//The maze wasn't meant for you
	if !isItMyPod {
		return
	}
	containersToBeSet := gatherAllContainers(pod)
	if len(containersToBeSet) > 0 {
		var err error
		for i := 0; i < MaxRetryCount; i++ {
			err = setHandler.adjustContainerSets(pod, containersToBeSet)
			if err == nil {
				return
			}
			time.Sleep(RetryInterval * time.Millisecond)
		}
		log.Println("ERROR: Timed out trying to adjust the cpusets of the containers belonging to Pod:" + pod.ObjectMeta.Name + " ID: " + string(pod.ObjectMeta.UID) + " because:" + err.Error())
	} else {
		log.Println("WARNING: there were no containers to handle in: " + pod.ObjectMeta.Name + " ID: " + string(pod.ObjectMeta.UID) + " in thread:" + strconv.Itoa(unix.Gettid()))
	}
}

func shouldPodBeHandled(pod v1.Pod) (bool, v1.Pod) {
	// Pod has exited/completed and all containers have stopped
	log.Println("pod 状态:", pod.Namespace, pod.Name, pod.Status.Phase)
	if pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed {
		return false, pod
	}
	setterNodeName := os.Getenv("NODE_NAME")
	for i := 0; i < MaxRetryCount; i++ {
		//We will unconditionally read the Pod at least once due to two reasons:
		//1: 99% Chance that the Pod arriving in the CREATE event is not yet ready to be processed
		//2: Avoid spending cycles on a Pod which does not even exist anymore in the API server
		newPod, err := k8sclient.RefreshPod(pod)
		if err != nil {
			log.Println("WARNING: Pod:" + pod.ObjectMeta.Name + " ID: " + string(pod.ObjectMeta.UID) + " is not adjusted as reading it again failed with:" + err.Error())
			return false, pod
		}
		if isPodReadyForProcessing(*newPod) {
			pod = *newPod
			break
		}
		time.Sleep(RetryInterval * time.Millisecond)
	}
	//Pod still haven't been scheduled, or it wasn't scheduled to the Node of this specific CPUSetter instance
	if setterNodeName != pod.Spec.NodeName {
		return false, pod
	}
	return true, pod
}

func isPodReadyForProcessing(pod v1.Pod) bool {
	if pod.Spec.NodeName == "" || len(pod.Status.ContainerStatuses) != len(pod.Spec.Containers) {
		return false
	}
	for _, cStatus := range pod.Status.ContainerStatuses {
		if cStatus.ContainerID == "" {
			//Pod might have been scheduled but its containers haven't been created yet
			return false
		}
	}
	return true
}

func gatherAllContainers(pod v1.Pod) map[string]int {
	workingContainers := map[string]int{}
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if containerStatus.ContainerID == "" {
			return map[string]int{}
		}
		workingContainers[containerStatus.Name] = 0
	}
	return workingContainers
}

func (setHandler *SetHandler) adjustContainerSets(pod v1.Pod, containersToBeSet map[string]int) error {
	var (
		pathToContainerCpusetFile string
		pathToContainerCpusFile   string
		err                       error
	)
	for _, container := range pod.Spec.Containers {
		if _, found := containersToBeSet[container.Name]; !found {
			continue
		}
		cfgContainerCPUSet, realCPUSet, err := setHandler.determineCorrectCpuset(pod, container)
		if err != nil {
			return errors.New("correct cpuset for the containers of Pod: " + pod.ObjectMeta.Name + " ID: " + string(pod.ObjectMeta.UID) + " could not be calculated in thread:" + strconv.Itoa(unix.Gettid()) + " because:" + err.Error())
		}
		containerID := determineCid(pod.Status, container.Name)
		if containerID == "" {
			return errors.New("cannot determine container ID of container: " + container.Name + " in Pod: " + pod.ObjectMeta.Name + " ID: " + string(pod.ObjectMeta.UID) + " in thread:" + strconv.Itoa(unix.Gettid()) + " because:" + err.Error())
		}
		pathToContainerCpusetFile, err = setHandler.applyCpusetToContainer(pod.ObjectMeta, containerID, realCPUSet)
		if err != nil {
			return errors.New("cpuset of container: " + container.Name + " in Pod: " + pod.ObjectMeta.Name + " ID: " + string(pod.ObjectMeta.UID) + " could not be re-adjusted in thread:" + strconv.Itoa(unix.Gettid()) + " because:" + err.Error())
		}
		pathToContainerCpusFile, err = setHandler.applyCpusToContainer(pod.ObjectMeta, container.Name, containerID, cfgContainerCPUSet)
		if err != nil {
			return errors.New("cpu of container: " + container.Name + " in Pod: " + pod.ObjectMeta.Name + " ID: " + string(pod.ObjectMeta.UID) + " could not be re-adjusted in thread:" + strconv.Itoa(unix.Gettid()) + " because:" + err.Error())
		}
		_ = pathToContainerCpusFile
	}
	err = setHandler.applyCpusetToInfraContainer(pod.ObjectMeta, pod.Status, pathToContainerCpusetFile)
	if err != nil {
		return errors.New("cpuset of the infra container in Pod: " + pod.ObjectMeta.Name + " ID: " + string(pod.ObjectMeta.UID) + " could not be re-adjusted in thread:" + strconv.Itoa(unix.Gettid()) + " because:" + err.Error())
	}
	err = k8sclient.SetPodAnnotation(&pod, setterAnnotationKey, "true")
	if err != nil {
		return errors.New("could not update annotation in Pod:" + pod.ObjectMeta.Name + " ID: " + string(pod.ObjectMeta.UID) + "  in thread:" + strconv.Itoa(unix.Gettid()) + " because: " + err.Error())
	}
	return nil
}

func (setHandler *SetHandler) determineCorrectCpuset(pod v1.Pod, container v1.Container) (cpuset.CPUSet, cpuset.CPUSet, error) {
	var (
		sharedCPUSet, exclusiveCPUSet, cloudphoneCPUSet cpuset.CPUSet
		err                                             error
	)
	for resourceName := range container.Resources.Requests {
		resNameAsString := string(resourceName)
		fmt.Println("resNameAsString:::", resNameAsString)
		if strings.Contains(resNameAsString, resourceBaseName) && strings.Contains(resNameAsString, types.SharedPoolID) {
			sharedCPUSet = setHandler.poolConfig.SelectPool(types.SharedPoolID).CPUset
		} else if strings.Contains(resNameAsString, resourceBaseName) && strings.Contains(resNameAsString, types.ExclusivePoolID) {
			exclusiveCPUSet, err = setHandler.getListOfAllocatedExclusiveCpus(resNameAsString, pod, container)
			if err != nil {
				return cpuset.CPUSet{}, cpuset.CPUSet{}, err
			}
			fullResName := strings.Split(resNameAsString, "/")
			exclusivePoolName := fullResName[1]
			fmt.Println("exclusivePoolName:::", exclusivePoolName)
			if setHandler.poolConfig.SelectPool(exclusivePoolName).HTPolicy == types.MultiThreadHTPolicy {
				htMap := topology.GetHTTopology()
				exclusiveCPUSet = topology.AddHTSiblingsToCPUSet(exclusiveCPUSet, htMap)
			}
		} else if strings.Contains(resNameAsString, resourceBaseName) && strings.Contains(resNameAsString, types.CloudphonePoolID) {
			cloudphoneCPUSet, err = setHandler.getListOfAllocatedExclusiveCpus(resNameAsString, pod, container)
			if err != nil {
				return cpuset.CPUSet{}, cpuset.CPUSet{}, err
			}
			fullResName := strings.Split(resNameAsString, "/")
			cloudphonePoolName := fullResName[1]
			fmt.Println("cloudphonePoolName:::", cloudphonePoolName, "cloudphoneCPUSet::", cloudphoneCPUSet.String())
			if cloudphoneCPUSet.IsEmpty() {
				fmt.Println("cloudphoneCPUSet 为空，返回")
				return cpuset.CPUSet{}, cpuset.CPUSet{}, errors.New("getListOfAllocatedExclusiveCpus cloudphoneCPUSet isEmpty!")
			}
			realCPUSet := cpuset.New()
			numaNodeIdx := setHandler.poolConfig.SelectPool(cloudphonePoolName).CoreMap[cloudphoneCPUSet.List()[0]].Node
			for _, v := range setHandler.poolConfig.SelectPool(cloudphonePoolName).CoreMap {
				if v.Node == numaNodeIdx {
					realCPUSet = realCPUSet.Union(realCPUSet, cpuset.New(v.Core))
				}
			}
			return cloudphoneCPUSet, realCPUSet, nil
		}
	}
	log.Println("determineCorrectCpuset:::", pod.Name, exclusiveCPUSet.String())
	if !sharedCPUSet.IsEmpty() || !exclusiveCPUSet.IsEmpty() {
		return sharedCPUSet.Union(exclusiveCPUSet), sharedCPUSet.Union(exclusiveCPUSet), nil
	}
	defaultCPUSet := setHandler.poolConfig.SelectPool(types.DefaultPoolID).CPUset
	return defaultCPUSet, defaultCPUSet, nil
}

// TODO: 使用 utils.GetCheckpointData 替代
func (setHandler *SetHandler) getListOfAllocatedExclusiveCpus(exclusivePoolName string, pod v1.Pod, container v1.Container) (cpuset.CPUSet, error) {
	checkpointFileName := "/var/lib/kubelet/device-plugins/kubelet_internal_checkpoint"
	buf, err := os.ReadFile(checkpointFileName)
	if err != nil {
		log.Printf("Error reading file %s: Error: %v", checkpointFileName, err)
		return cpuset.CPUSet{}, fmt.Errorf("kubelet checkpoint file could not be accessed because: %s", err)
	}
	var cp checkpoint.File
	if err = json.Unmarshal(buf, &cp); err != nil {
		//K8s 1.21 changed internal file structure, so let's try that too before returning with error
		var newCpFile checkpoint.NewFile
		if err = json.Unmarshal(buf, &newCpFile); err != nil {
			log.Printf("error unmarshalling kubelet checkpoint file: %s", err)
			return cpuset.CPUSet{}, err
		}
		cp = checkpoint.TranslateNewCheckpointToOld(newCpFile)
	}
	podIDStr := string(pod.ObjectMeta.UID)
	deviceIDs := []string{}
	for _, entry := range cp.Data.PodDeviceEntries {
		if entry.PodUID == podIDStr && entry.ContainerName == container.Name &&
			entry.ResourceName == exclusivePoolName {
			deviceIDs = append(deviceIDs, entry.DeviceIDs...)
		}
	}
	if len(deviceIDs) == 0 {
		log.Printf("WARNING: Container: %s in Pod: %s asked for exclusive CPUs, but were not allocated any! Cannot adjust its default cpuset", container.Name, podIDStr)
		return cpuset.CPUSet{}, nil
	}
	return calculateFinalExclusiveSet(deviceIDs, pod, container)
}

func calculateFinalExclusiveSet(exclusiveCpus []string, pod v1.Pod, container v1.Container) (cpuset.CPUSet, error) {
	setBuilder := cpuset.New()
	for _, deviceID := range exclusiveCpus {
		deviceIDasInt, err := strconv.Atoi(deviceID)
		if err != nil {
			return cpuset.CPUSet{}, err
		}
		setBuilder = setBuilder.Union(cpuset.New(deviceIDasInt % numOfCpus))
	}
	return setBuilder, nil
}

func determineCid(podStatus v1.PodStatus, containerName string) string {
	for _, containerStatus := range podStatus.ContainerStatuses {
		if containerStatus.Name == containerName {
			return trimContainerPrefix(containerStatus.ContainerID)
		}
	}
	return ""
}

func trimContainerPrefix(contName string) string {
	for _, prefix := range containerPrefixList {
		if strings.HasPrefix(contName, prefix) {
			return strings.TrimPrefix(contName, prefix)
		}
	}
	return contName
}

func containerIDInPodStatus(podStatus v1.PodStatus, containerDirName string) bool {
	for _, containerStatus := range podStatus.ContainerStatuses {
		trimmedCid := trimContainerPrefix(containerStatus.ContainerID)
		if strings.Contains(containerDirName, trimmedCid) {
			return true
		}
	}
	return false
}

// 解压 CPU.tar 到容器内
func (setHandler *SetHandler) applyCpusToContainer(podMeta metav1.ObjectMeta, containerName, containerID string, cpuSet cpuset.CPUSet) (string, error) {
	log.Println("解压 CPU.tar", podMeta.Name, containerID)
	defer func() {
		log.Println("解压 CPU.tar 完成", podMeta.Name, containerID)
	}()

	containerCPURootBase := filepath.Join(setHandler.cpuRoot, podMeta.Namespace, podMeta.Name, containerName)
	if exists, _ := utils.PathExists(containerCPURootBase); !exists {
		if err := os.MkdirAll(containerCPURootBase, 0755); err != nil {
			log.Println("创建失败", err)
			return "", err
		}
	}

	log.Println("容器目录:", containerCPURootBase)

	// 删除已存在目录
	empty, _ := utils.IsEmpty(containerCPURootBase)
	if !empty {
		entries, err := os.ReadDir(containerCPURootBase)
		if err != nil {
			return "", err
		}

		for _, entry := range entries {
			err := os.RemoveAll(filepath.Join(containerCPURootBase, entry.Name()))
			if err != nil {
				log.Println("删除失败", err)
			}
		}
	}

	tmpDir, err := temp.CreateTempDir(containerID)
	if err != nil {
		return "", err
	}
	defer tmpDir.Delete()
	file, err := utils.CPUFS.Open(utils.CPUInfrastructure)
	if err != nil {
		return "", err
	}
	if err := utils.UnTarGZ(tmpDir.Name, file); err != nil {
		log.Println("CPU 解压失败", err)
		return "", err
	}

	if err := utils.CopyDir(filepath.Join(tmpDir.Name, "cpu"), containerCPURootBase); err != nil {
		log.Println("CPU 目录拷贝失败", tmpDir.Name, containerCPURootBase, err)
		return "", err
	}

	var virtualCpuSet = cpuset.New(0)
	log.Println("cpuSet::", cpuSet.String(), tmpDir.Name)
	if len(cpuSet.List()) > 1 {
		for id, _ := range cpuSet.List()[1:] {
			fmt.Println("拷贝目录 debug:", id)
			virtualCpuSet = virtualCpuSet.Union(cpuset.New(id + 1))
			cpuZeroDir := filepath.Join(containerCPURootBase, "cpu0")
			cpuNumDir := filepath.Join(containerCPURootBase, fmt.Sprintf("cpu%d", id+1))
			if err := os.MkdirAll(cpuNumDir, 0755); err != nil {
				log.Println("创建目录失败", err)
				return "", err
			}
			if err := utils.CopyDir(cpuZeroDir, cpuNumDir); err != nil {
				log.Println("CPUID 拷贝失败", err)
				return "", err
			}
		}
	}

	filePath := filepath.Join(containerCPURootBase, "kernel_max")
	if err := os.WriteFile(filePath, []byte(strconv.Itoa(len(cpuSet.List())-1)), 0644); err != nil {
		log.Println("CPU 数量写入失败", err)
		return "", err
	}

	for _, file := range []string{"online", "possible", "present"} {
		filePath := filepath.Join(containerCPURootBase, file)
		if err := os.WriteFile(filePath, []byte(virtualCpuSet.String()), 0644); err != nil {
			log.Println("CPU 数量写入失败", err)
			return "", err
		}
	}
	return "", nil
}

func (setHandler *SetHandler) applyCpusetToContainer(podMeta metav1.ObjectMeta, containerID string, containerCpuset cpuset.CPUSet) (string, error) {
	if containerCpuset.IsEmpty() {
		//Nothing to set. We will leave the container running on the Kubernetes provisioned default cpuset
		log.Println("WARNING: cpuset to set was quite empty for container:" + containerID + " in Pod:" + podMeta.Name + " ID:" + string(podMeta.UID) + " in thread:" + strconv.Itoa(unix.Gettid()) + ". I left it untouched.")
		return "", nil
	}
	var pathToContainerCpusetFile string
	err := filepath.Walk(setHandler.cpusetRoot, func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(path, containerID) {
			pathToContainerCpusetFile = path
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("%s cpuset path error: %s", containerID, err.Error())
	}
	if pathToContainerCpusetFile == "" {
		return "", fmt.Errorf("cpuset file does not exist for container: %s under the provided cgroupfs hierarchy: %s", containerID, setHandler.cpusetRoot)
	}
	returnContainerPath := pathToContainerCpusetFile
	//And for our grand finale, we just "echo" the calculated cpuset to the cpuset cgroupfs "file" of the given container
	//Find child cpuset if it exists (kube-proxy)
	err = filepath.Walk(pathToContainerCpusetFile, func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if f.IsDir() {
			pathToContainerCpusetFile = path
		}
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("%s child cpuset path error: %s", containerID, err.Error())
	}
	err = os.WriteFile(pathToContainerCpusetFile+"/cpuset.cpus", []byte(containerCpuset.String()), 0755)
	if err != nil {
		return "", fmt.Errorf("can't modify cpuset file: %s for container: %s because: %s", pathToContainerCpusetFile, containerID, err)
	}
	return returnContainerPath, nil
}

func getInfraContainerPath(podStatus v1.PodStatus, searchPath string) string {
	var pathToInfraContainer string
	filelist, _ := filepath.Glob(filepath.Dir(searchPath) + "/*")
	for _, fpath := range filelist {
		fstat, err := os.Stat(fpath)
		if err != nil {
			continue
		}
		if fstat.IsDir() && !containerIDInPodStatus(podStatus, fstat.Name()) {
			pathToInfraContainer = fpath
		}
	}
	return pathToInfraContainer
}

func (setHandler *SetHandler) applyCpusetToInfraContainer(podMeta metav1.ObjectMeta, podStatus v1.PodStatus, pathToSearchContainer string) error {
	cpuset := setHandler.poolConfig.SelectPool(types.DefaultPoolID).CPUset
	if cpuset.IsEmpty() {
		//Nothing to set. We will leave the container running on the Kubernetes provisioned default cpuset
		log.Println("WARNING: DEFAULT cpuset to set was quite empty in Pod:" + podMeta.Name + " ID:" + string(podMeta.UID) + " in thread:" + strconv.Itoa(unix.Gettid()) + ". I left it untouched.")
		return nil
	}
	if pathToSearchContainer == "" {
		return fmt.Errorf("container directory does not exists under the provided cgroupfs hierarchy: %s", setHandler.cpusetRoot)
	}
	pathToContainerCpusetFile := getInfraContainerPath(podStatus, pathToSearchContainer)
	if pathToContainerCpusetFile == "" {
		return fmt.Errorf("cpuset file does not exist for infra container under the provided cgroupfs hierarchy: %s", setHandler.cpusetRoot)
	}
	// 可以根据需要，写入当前分配 core 所在 numa 节点的全部 core。比如分配的是 4-7，可写入当前 numa 节点的 4-31
	err := os.WriteFile(pathToContainerCpusetFile+"/cpuset.cpus", []byte(cpuset.String()), 0755)
	if err != nil {
		return fmt.Errorf("can't modify cpuset file: %s for infra container: %s because: %s", pathToContainerCpusetFile, filepath.Base(pathToContainerCpusetFile), err)
	}
	return nil
}

func (setHandler *SetHandler) startReconciliationLoop() {
	timeToReconcile := time.NewTicker(5 * time.Second)
	for {
		select {
		case <-timeToReconcile.C:
			err := setHandler.reconcileCpusets()
			if err != nil {
				log.Println("WARNING: Periodic cpuset reconciliation failed with error:" + err.Error())
				continue
			}
		case <-*setHandler.stopChan:
			log.Println("INFO: Shutting down the periodic cpuset reconciliation thread")
			timeToReconcile.Stop()
			return
		}
	}
}

func (setHandler *SetHandler) reconcileCpusets() error {
	pods, err := k8sclient.GetPodsFromKubelet()
	if pods == nil || err != nil {
		return errors.New("couldn't List my Pods in the reconciliation loop because:" + err.Error())
	}
	leafCpusets, err := setHandler.getLeafCpusets()
	if err != nil {
		return errors.New("couldn't interrogate leaf cpusets from cgroupfs because:" + err.Error())
	}
	for _, pod := range pods.Items {
		for _, container := range pod.Spec.Containers {
			err = setHandler.reconcileContainer(leafCpusets, pod, container)
			if err != nil {
				log.Println("WARNING: Periodic reconciliation of container:" + container.Name + " of Pod:" + pod.ObjectMeta.Name + " in namespace:" + pod.ObjectMeta.Namespace + " failed with error:" + err.Error())
			}
		}
	}
	return nil
}

func (setHandler *SetHandler) getLeafCpusets() ([]string, error) {
	stdOut, err := topology.ExecCommand(exec.Command("find", setHandler.cpusetRoot, "-type", "d", "-links", "2"))
	if err != nil {
		return nil, err
	}
	cpusetLeaves := strings.Split(strings.TrimSuffix(stdOut, "\n"), "\n")
	return cpusetLeaves, nil
}

// Naive approach: we can prob afford not building a tree from the cgroup paths if we only reconcile every couple of seconds
// Can be further optimized on need
func (setHandler *SetHandler) reconcileContainer(leafCpusets []string, pod v1.Pod, container v1.Container) error {
	containerID := determineCid(pod.Status, container.Name)
	if containerID == "" {
		return nil
	}
	badCpuset, _ := cpuset.Parse("0-" + strconv.Itoa(numOfCpus-1))
	for _, leaf := range leafCpusets {
		if strings.Contains(leaf, containerID) {
			currentCpusetByte, _ := os.ReadFile(leaf + "/cpuset.cpus")
			currentCpusetStr := strings.TrimSpace(string(currentCpusetByte))
			currentCpuset, _ := cpuset.Parse(currentCpusetStr)
			if badCpuset.Equals(currentCpuset) {
				configCPUSet, correctSet, err := setHandler.determineCorrectCpuset(pod, container)
				if err != nil {
					return errors.New("could not determine correct cpuset because:" + err.Error())
				}
				log.Println("写入 cpuset.cpus", pod.Namespace, pod.Name, container.Name)
				path, err := setHandler.applyCpusToContainer(pod.ObjectMeta, container.Name, containerID, configCPUSet)
				log.Println("生成CPU文件:", path, err)
				err = os.WriteFile(leaf+"/cpuset.cpus", []byte(correctSet.String()), 0755)
				if err != nil {
					return errors.New("could not overwrite cpuset file:" + leaf + "/cpuset.cpus because:" + err.Error())
				}
			}
			break
		}
	}
	return nil
}
