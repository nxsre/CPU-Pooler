package k8sclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-resty/resty/v2"
	jsoniter "github.com/json-iterator/go"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/transport"
	"k8s.io/client-go/util/homedir"
	"net/url"
	"os"
	"path/filepath"
	"sync"
)

var (
	thisNode *v1.Node
	once     sync.Once
)

type Metadata struct {
	Annotations map[string]json.RawMessage `json:"annotations"`
}

type update struct {
	Metadata Metadata `json:"metadata"`
}

// GetNodeLabels returns node labels.
// NODE_NAME environment variable is used to determine the node
func GetNodeLabels() (map[string]string, error) {
	cSet, err := createClientSet()
	if err != nil {
		return nil, err
	}
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		return nil, nil
	}
	nodes, err := cSet.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return nodes.ObjectMeta.Labels, nil
}

// SetPodAnnotation adds or modifies annotation for pod
func SetPodAnnotation(pod *v1.Pod, key string, value string) error {
	cSet, err := createClientSet()
	if err != nil {
		return err
	}
	merge := update{}
	merge.Metadata.Annotations = make(map[string]json.RawMessage)
	merge.Metadata.Annotations[key] = json.RawMessage(`"` + value + `"`)

	jsonData, err := json.Marshal(merge)
	if err != nil {
		return err
	}
	_, err = cSet.CoreV1().Pods(pod.ObjectMeta.Namespace).Patch(context.TODO(), pod.ObjectMeta.Name, types.MergePatchType, jsonData, metav1.PatchOptions{})
	return err
}

// RefreshPod takes an existing Pod object as an input, and re-reads it from the K8s API
// Returns the refreshed Pod descriptor in case of success, or an error
func RefreshPod(pod v1.Pod) (*v1.Pod, error) {
	cSet, err := createClientSet()
	if err != nil {
		return nil, err
	}
	return cSet.CoreV1().Pods(pod.ObjectMeta.Namespace).Get(context.TODO(), pod.ObjectMeta.Name, metav1.GetOptions{})
}

func GetRunningContainer(pod *v1.Pod, containerName string) (*v1.ContainerStatus, error) {
	cSet, err := createClientSet()
	if err != nil {
		return nil, err
	}

	pod, err = RefreshPod(*pod)
	if err != nil {
		return nil, err
	}

	fmt.Println("Watch Kubernetes Pods in CrashLoopBackOff state")
	watcher, err := cSet.CoreV1().Pods(pod.Namespace).Watch(context.Background(),
		metav1.ListOptions{
			ResourceVersion: pod.ResourceVersion,
			FieldSelector:   fields.Set{"metadata.name": pod.Name}.AsSelector().String(),
			LabelSelector:   labels.Everything().String(),
		})
	if err != nil {
		fmt.Printf("error create pod watcher: %v\n", err)
		return nil, err
	}

	defer watcher.Stop()

	for event := range watcher.ResultChan() {
		pod, ok := event.Object.(*v1.Pod)
		if !ok {
			continue
		}
		for _, c := range pod.Status.ContainerStatuses {
			if !c.Ready {
				if c.State.Waiting != nil {
					fmt.Printf("PodName: %s, Namespace: %s, Phase: %s WaitingReason: %s \n", pod.ObjectMeta.Name, pod.ObjectMeta.Namespace, pod.Status.Phase, c.State.Waiting.Reason)
				}
			}
			if c.Name == containerName && c.Ready {
				return &c, nil
			}

		}
	}
	return nil, err
}

func GetPod(namespace, podName string) (*v1.Pod, error) {
	cSet, err := createClientSet()
	if err != nil {
		return nil, err
	}
	return cSet.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
}

func GetPVC(namespace, pvcName string) (*v1.PersistentVolumeClaim, error) {
	cSet, err := createClientSet()
	if err != nil {
		return nil, err
	}
	return cSet.CoreV1().PersistentVolumeClaims(namespace).Get(context.TODO(), pvcName, metav1.GetOptions{})
}

func GetPV(pvName string) (*v1.PersistentVolume, error) {
	cSet, err := createClientSet()
	if err != nil {
		return nil, err
	}
	return cSet.CoreV1().PersistentVolumes().Get(context.TODO(), pvName, metav1.GetOptions{})
}

func PatchPVReclaimPolicy(pvName string, policy v1.PersistentVolumeReclaimPolicy) (*v1.PersistentVolume, error) {
	cSet, err := createClientSet()
	if err != nil {
		return nil, err
	}

	data := fmt.Sprintf(`{"spec":{"persistentVolumeReclaimPolicy":"%s"}}`, policy)
	return cSet.CoreV1().PersistentVolumes().Patch(context.TODO(), pvName, types.StrategicMergePatchType, []byte(data), metav1.PatchOptions{})
}

func PatchPVClearClaimRef(pvName string) (*v1.PersistentVolume, error) {
	cSet, err := createClientSet()
	if err != nil {
		return nil, err
	}

	data := fmt.Sprintf(`{"spec":{"claimRef": null}}`)
	return cSet.CoreV1().PersistentVolumes().Patch(context.TODO(), pvName, types.StrategicMergePatchType, []byte(data), metav1.PatchOptions{})
}

// GetMyPods returns all the Pods to the caller running on the same node as this process
// Node is identified by the NODE_NAME environment variable. The Pods are filtered based on their spec.nodeName attribute
func GetMyPods() (*v1.PodList, error) {
	cSet, err := createClientSet()
	if err != nil {
		return nil, err
	}
	nodeName := os.Getenv("NODE_NAME")
	// kubectl get pod  --field-selector spec.nodeName=${NODE_NAME} -A -o wide
	return cSet.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{FieldSelector: "spec.nodeName=" + nodeName})
}

func GetThisNode() *v1.Node {
	once.Do(func() {
		var err error
		nodeName := os.Getenv("NODE_NAME")
		if nodeName == "" {
			hostname, err := os.Hostname()
			if err != nil {
				return
			}
			nodeName = hostname
		}

		cSet, err := createClientSet()
		if err != nil {
			return
		}

		thisNode, err = cSet.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
		if err != nil {
			return
		}
	})
	return thisNode
}

// GetPodsFromKubelet returns all the Pods to the caller running on the same node as this process
func GetPodsFromKubelet() (*v1.PodList, error) {
	config, err := createRestConfig()
	if err != nil {
		return nil, err
	}

	node := GetThisNode()
	if err != nil {
		return nil, err
	}

	// 获取 kubelet IP
	var ipAddress string
	for _, ipInfo := range node.Status.Addresses {
		if ipInfo.Type == v1.NodeInternalIP {
			ipAddress = ipInfo.Address
		}
	}

	transportConfig, err := config.TransportConfig()
	if err != nil {
		return nil, err
	}

	var client *resty.Client
	tlsConfig, err := transport.TLSConfigFor(transportConfig)
	if err != nil {
		return nil, err
	}
	tlsConfig.InsecureSkipVerify = true
	client = resty.New().SetTLSClientConfig(tlsConfig)

	if transportConfig.HasTokenAuth() {
		client.SetHeader("Authorization", fmt.Sprintf("Bearer %s", config.BearerToken))
	}

	resp, err := client.R().Get(fmt.Sprintf("https://%s:%d/pods", ipAddress, node.Status.DaemonEndpoints.KubeletEndpoint.Port))
	if err != nil {
		return nil, err
	}
	var podList = &v1.PodList{}
	if err := jsoniter.Unmarshal(resp.Body(), podList); err != nil {
		return nil, err
	}
	return podList, nil
}

// GetPodsFromKubelet returns all the Pods to the caller running on the same node as this process
func GetMetricsFromKubelet(cadvisor bool) (map[string]*dto.MetricFamily, error) {
	config, err := createRestConfig()
	if err != nil {
		return nil, err
	}

	node := GetThisNode()
	// 获取 kubelet IP
	var ipAddress string
	for _, ipInfo := range node.Status.Addresses {
		if ipInfo.Type == v1.NodeInternalIP {
			ipAddress = ipInfo.Address
		}
	}

	transportConfig, err := config.TransportConfig()
	if err != nil {
		return nil, err
	}

	var client *resty.Client
	tlsConfig, err := transport.TLSConfigFor(transportConfig)
	if err != nil {
		return nil, err
	}
	tlsConfig.InsecureSkipVerify = true
	client = resty.New().SetTLSClientConfig(tlsConfig)

	if transportConfig.HasTokenAuth() {
		client.SetHeader("Authorization", fmt.Sprintf("Bearer %s", config.BearerToken))
	}

	metricsUri := fmt.Sprintf("https://%s:%d/metrics", ipAddress, node.Status.DaemonEndpoints.KubeletEndpoint.Port)
	if cadvisor {
		metricsUri, _ = url.JoinPath(metricsUri, "cadvisor")
	}

	resp, err := client.R().Get(metricsUri)
	if err != nil {
		return nil, err
	}
	//dec := expfmt.NewDecoder(resp.RawBody(), expfmt.Format(resp.Header().Get("Content-Type")))
	//mf := &dto.MetricFamily{}
	//dec.Decode(mf)
	//fmt.Println(resp)
	var parser expfmt.TextParser
	return parser.TextToMetricFamilies(bytes.NewReader(resp.Body()))
}

func ClientSet() (*kubernetes.Clientset, error) {
	return createClientSet()
}

func RestConfig() (*rest.Config, error) {
	return createRestConfig()
}

func createClientSet() (*kubernetes.Clientset, error) {
	config, err := createRestConfig()
	if err != nil {
		return nil, err
	}
	//config.TransportConfig()
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return clientset, nil
}

func createRestConfig() (*rest.Config, error) {
	//.kube/config文件存在，就使用文件
	var kubeConfigFilePath string
	if home := homedir.HomeDir(); home != "" {
		kubeConfigFilePath = filepath.Join(home, ".kube", "config")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeConfigFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			//当程序以pod方式运行时，就直接走这里的逻辑
			config, err = rest.InClusterConfig()
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	return config, nil
}
