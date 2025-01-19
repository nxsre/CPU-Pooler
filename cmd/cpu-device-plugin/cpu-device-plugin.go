package main

import (
	"flag"
	"fmt"
	jsoniter "github.com/json-iterator/go"
	"github.com/nokia/CPU-Pooler/pkg/utils"
	"github.com/smallnest/weighted"
	"google.golang.org/grpc/credentials/insecure"
	"log"
	"math"
	"net"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/golang/glog"
	"github.com/nokia/CPU-Pooler/pkg/types"
	"github.com/nxsre/toolkit/pkg/topology"
	"golang.org/x/net/context"
	grpc "google.golang.org/grpc"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	"k8s.io/utils/cpuset"
)

var (
	resourceBaseName = "bonc.k8s.io"
	cdms             []*cpuDeviceManager
)

type cpuDeviceManager struct {
	pool               types.Pool
	socketFile         string
	devicePluginPath   string
	grpcServer         *grpc.Server
	sharedPoolCPUs     string
	poolType           string
	nodeTopology       map[int]int // core node 对应关系
	htTopology         map[int]string
	cloudphoneCPU      weighted.W
	cloudphoneExtDevs  map[int]*weighted.SW
	cloudphoneCPUTopol map[int]topology.Topology
	cacheDevices       map[string]pluginapi.Device
	numCPU             int
}

// TODO: PoC if cpuset setting could be implemented in this hook? cpuset cgroup of the container should already exist at this point (kinda)
// The DeviceIDs could be used to determine which container has them, once we have a container name parsed out from the allocation backend we could manipulate its cpuset before it is even started
// Long shot, but if it works both cpusetter and process starter would become unnecessary
func (cdm *cpuDeviceManager) PreStartContainer(ctx context.Context, psRqt *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	log.Printf("psRqt :%+v", psRqt.String())
	//ckpoint, err := utils.GetCheckpointData(cdm.poolType)
	//if err != nil {
	//	fmt.Println("PreStartContainer", err)
	//	return nil, err
	//}
	//fmt.Printf("ckpoint: %+v", ckpoint)
	resp := &pluginapi.PreStartContainerResponse{}
	return resp, nil
}

func (cdm *cpuDeviceManager) Start() error {
	pluginEndpoint := filepath.Join(pluginapi.DevicePluginPath, cdm.socketFile)
	if err := os.Remove(pluginEndpoint); err != nil {
		fmt.Println("删除 socketfile", pluginEndpoint, err)
	}
	glog.Infof("Starting CPU Device Plugin server at: %s\n", pluginEndpoint)
	lis, err := net.Listen("unix", pluginEndpoint)
	if err != nil {
		glog.Errorf("Error. Starting CPU Device Plugin server failed: %v", err)
	}
	cdm.grpcServer = grpc.NewServer()

	// Register all services
	pluginapi.RegisterDevicePluginServer(cdm.grpcServer, cdm)

	go cdm.grpcServer.Serve(lis)

	// Wait for server to start by launching a blocking connection
	log.Println("cpuDeviceManager start, pluginEndpoint:", pluginEndpoint)
	conn, err := grpc.NewClient(fmt.Sprintf("unix://%s", pluginEndpoint), grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			// 使用自定义 Dialer 实现 WithBlock() 方法
			dialer := utils.NewMyDialer(utils.RetryOption(50), utils.TimeoutOption(5*time.Second))
			network, endpoint := utils.ParseDialTarget(addr)
			conn, err := dialer.DialContext(ctx, network, endpoint)
			return conn, err
		}),
	)
	if err != nil {
		glog.Errorf("Error. Could not establish connection with gRPC server: %v", err)
		return err
	}
	glog.Infoln("CPU Device Plugin server started serving")
	conn.Close()
	return nil
}

func (cdm *cpuDeviceManager) cleanup() error {
	pluginEndpoint := filepath.Join(pluginapi.DevicePluginPath, cdm.socketFile)
	if err := os.Remove(pluginEndpoint); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (cdm *cpuDeviceManager) Stop() error {
	glog.Infof("CPU Device Plugin gRPC server..")
	if cdm.grpcServer == nil {
		return nil
	}
	cdm.grpcServer.Stop()
	cdm.grpcServer = nil
	return cdm.cleanup()
}

// ListAndWatch 注册 CPU 数量
func (cdm *cpuDeviceManager) ListAndWatch(e *pluginapi.Empty, stream pluginapi.DevicePlugin_ListAndWatchServer) error {
	var updateNeeded = true
	for {
		if updateNeeded {
			cacheDevices := make(map[string]pluginapi.Device)
			resp := new(pluginapi.ListAndWatchResponse)
			if cdm.poolType == "shared" {
				nbrOfCPUs := cdm.pool.CPUset.Size()
				for i := 0; i < nbrOfCPUs*1000; i++ {
					cpuID := strconv.Itoa(i)
					resp.Devices = append(resp.Devices, &pluginapi.Device{ID: cpuID, Health: pluginapi.Healthy})
				}
			} else {
				//glog.Infoln("nodeTopology::", cdm.nodeTopology)
				for _, cpuID := range cdm.pool.CPUset.List() {
					// 根据超售率设置 CPU Core 数量
					if cdm.pool.Overcommitment < 1 {
						cdm.pool.Overcommitment = 1
					}
					for overID := 0; overID < cdm.pool.Overcommitment; overID++ {
						exclusiveCore := pluginapi.Device{ID: strconv.Itoa(cpuID + overID*cdm.numCPU), Health: pluginapi.Healthy}
						//glog.Infoln("CPU信息: ", cpuID, overID*cdm.numCPU, cpuID+overID*cdm.numCPU)
						if numaNode, exists := cdm.nodeTopology[cpuID]; exists {
							exclusiveCore.Topology = &pluginapi.TopologyInfo{Nodes: []*pluginapi.NUMANode{{ID: int64(numaNode)}}}
						}
						cacheDevices[exclusiveCore.ID] = exclusiveCore
						resp.Devices = append(resp.Devices, &exclusiveCore)
					}
				}
			}
			cdm.cacheDevices = cacheDevices
			if err := stream.Send(resp); err != nil {
				glog.Errorf("Error. Cannot update device states: %v\n", err)
				return err
			}
			updateNeeded = false
		}
		//TODO: When is update needed ?
		time.Sleep(5 * time.Second)
	}
	return nil

}

func (cdm *cpuDeviceManager) Allocate(ctx context.Context, rqt *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	resp := new(pluginapi.AllocateResponse)
	for _, container := range rqt.ContainerRequests {
		envmap := make(map[string]string)
		cpusAllocated, _ := cpuset.Parse("")
		for _, id := range container.DevicesIDs {
			idN, err := strconv.ParseUint(id, 10, 32)
			if err != nil {
				continue
			}
			tempSet := cpuset.New(int(idN) % cdm.numCPU)
			cpusAllocated = cpusAllocated.Union(tempSet)
		}
		if cdm.pool.HTPolicy == types.MultiThreadHTPolicy {
			cpusAllocated = topology.AddHTSiblingsToCPUSet(cpusAllocated, cdm.htTopology)
		}
		if cdm.poolType == "shared" {
			envmap["SHARED_CPUS"] = cdm.sharedPoolCPUs
		} else if cdm.poolType == "cloudphone" {
			envmap["CLOUDPHONE_CPUS"] = cpusAllocated.String()
			cpus := []int{}
			for cpu, topol := range cdm.pool.CoreMap {
				if cdm.cloudphoneCPUTopol[cpusAllocated.List()[0]].Node == topol.Node {
					cpus = append(cpus, cpu)
				}
			}
			envmap["NUMA_CPUS"] = cpuset.New(cpus...).String()
		} else {
			envmap["EXCLUSIVE_CPUS"] = cpusAllocated.String()
		}
		containerResp := new(pluginapi.ContainerAllocateResponse)
		glog.Infof("Container: %v, CPUs allocated: %s: Num of CPUs %s",
			container.String(),
			cpusAllocated.String(),
			strconv.Itoa(cpusAllocated.Size()))

		// 通过环境变量与 setter 同步状态，/sys/fs/cgroup/cpuset/kubepods/xxx/xxxx/cpuset.cpus
		containerResp.Envs = envmap

		// TODO: 瀚博会走到这里，分配异常检查断言是否正确以及权限是否正确
		if extDev, ok := cdm.cloudphoneExtDevs[cdm.nodeTopology[cpusAllocated.List()[0]]]; ok {
			topol, ok := extDev.Next().(*topology.Topology)
			if ok {
				for _, v := range topol.Devices {
					containerResp.Devices = append(containerResp.Devices, &pluginapi.DeviceSpec{
						ContainerPath: v,
						HostPath:      v,
						Permissions:   "rwm",
					})
				}
			}
		}

		// netint 和 amdgpu 在这里分配, 和瀚博设备不排斥
		for _, dev := range cdm.cloudphoneCPUTopol[cpusAllocated.List()[0]].Devices {
			fmt.Println(cdm.cloudphoneCPUTopol[cpusAllocated.List()[0]])
			containerPath := dev
			if strings.HasPrefix(dev, "/dev/dri/renderD") {
				containerPath = "/dev/dri/renderD128"
			}

			containerResp.Devices = append(containerResp.Devices, &pluginapi.DeviceSpec{
				ContainerPath: containerPath,
				HostPath:      dev,
				Permissions:   "rwm",
			})
		}

		jb, err := jsoniter.MarshalIndent(containerResp, "", "  ")
		if err != nil {
			fmt.Println(err)
		} else {
			fmt.Println("debug:::: -->")
			fmt.Println(string(jb))
		}

		resp.ContainerResponses = append(resp.ContainerResponses, containerResp)
	}
	return resp, nil
}

func (cdm *cpuDeviceManager) GetDevicePluginOptions(context.Context, *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	dpOptions := pluginapi.DevicePluginOptions{
		PreStartRequired:                true, // 开启 PreStartContainer
		GetPreferredAllocationAvailable: true, // 开启 GetPreferredAllocation
	}
	return &dpOptions, nil
}

func (cdm *cpuDeviceManager) Register(kubeletEndpoint, resourceName string) error {
	log.Println(kubeletEndpoint)
	conn, err := grpc.NewClient(fmt.Sprintf("unix://%s", kubeletEndpoint), grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			glog.Warning("grpc.Dial addr: %s", addr)
			network, endpoint := utils.ParseDialTarget(addr)
			return utils.NewMyDialer().DialContext(ctx, network, endpoint)
		}),
	)

	if err != nil {
		glog.Errorf("CPU Device Plugin cannot connect to Kubelet service: %v", err)
		return err
	}
	defer conn.Close()
	client := pluginapi.NewRegistrationClient(conn)

	request := &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     cdm.socketFile,
		ResourceName: resourceName,
	}

	if _, err = client.Register(context.Background(), request); err != nil {
		glog.Errorf("CPU Device Plugin cannot register to Kubelet service: %v", err)
		return err
	}
	return nil
}

// GetPreferredAllocation 优选 CPU, 用于优化 CPU 复用分配
func (cdm *cpuDeviceManager) GetPreferredAllocation(ctx context.Context, req *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	//log.Printf("req %+v", req.String())
	resp := &pluginapi.PreferredAllocationResponse{}

	type numaT struct {
		weight int
		ids    []string
	}

	for _, c := range req.ContainerRequests {
		preferredIDs := []string{}
		idSet := map[int]struct{}{}
		// 传进来的 c.AvailableDeviceIDs 是乱序的，CPU 绑定numa 要先排序
		slices.SortFunc(c.AvailableDeviceIDs, func(a, b string) int {
			an, _ := strconv.ParseUint(a, 10, 32)
			bn, _ := strconv.ParseUint(b, 10, 32)
			return int(bn) - int(an)
		})

		log.Println("ContainerRequest::", cdm.nodeTopology, len(c.AvailableDeviceIDs), c.AllocationSize)
		// 根据 numa 拓扑分配 CPU, 传入的 AvailableDeviceIDs 表示当前节点空闲的设备ID
		numaSW := &weighted.SW{}
		numaMap := map[int64]numaT{}
		for _, id := range c.AvailableDeviceIDs {
			// 计算每个 numa 节点的权重
			core, ok := numaMap[cdm.cacheDevices[id].Topology.Nodes[0].GetID()]
			if !ok {
				core = numaT{
					weight: 0,
					ids:    []string{id},
				}
			} else {
				core.weight = core.weight + 1
				core.ids = append(core.ids, id)
			}
			numaMap[cdm.cacheDevices[id].Topology.Nodes[0].GetID()] = core
		}

		for k, numa := range numaMap {
			numaSW.Add(k, numa.weight)
		}

		fmt.Println("debug numaSW:", numaSW.All())
		numa, ok := numaSW.Next().(int64)
		if !ok {
			fmt.Println("类型错误")
			return resp, nil
		}

		slices.SortFunc(numaMap[numa].ids, func(a, b string) int {
			an, _ := strconv.ParseUint(a, 10, 32)
			bn, _ := strconv.ParseUint(b, 10, 32)
			return int(bn) - int(an)
		})

		for _, n := range numaMap[numa].ids {
			id, err := strconv.Atoi(n)
			if err != nil {
				continue
			}
			if _, exists := idSet[id%cdm.numCPU]; exists {
				continue
			}

			if len(preferredIDs) > 0 {
				preId, _ := strconv.ParseFloat(preferredIDs[len(preferredIDs)-1], 10)
				if cdm.nodeTopology[id%cdm.numCPU] != cdm.nodeTopology[int(preId)%cdm.numCPU] {
					glog.Warningf("%v 跨numa，重新分配", append(preferredIDs, strconv.Itoa(id)))
					preferredIDs = nil
					idSet = map[int]struct{}{}
				}

				if int(math.Abs(preId-float64(id))) != 1 {
					// 如果当前 numa node 不够分配则 重来
					glog.Infof("%v 不连续，重新分配", append(preferredIDs, strconv.Itoa(id)))
					preferredIDs = nil
					idSet = map[int]struct{}{}
				}
			}

			preferredIDs = append(preferredIDs, strconv.Itoa(id))
			idSet[id%cdm.numCPU] = struct{}{}

			if len(preferredIDs) == int(c.AllocationSize) {
				break
			}

		}
		resp.ContainerResponses = append(resp.ContainerResponses, &pluginapi.ContainerPreferredAllocationResponse{DeviceIDs: preferredIDs})
	}
	return resp, nil
}

func newCPUDeviceManager(poolName string, pool types.Pool, sharedCPUs string) *cpuDeviceManager {
	glog.Infof("Starting plugin for pool: %s", poolName)
	numaWeight, err := topology.ConvertWeight(pool.NumaWeight)
	if err != nil {
		fmt.Println("NumaWeight 参数错误:", pool.NumaWeight, err)
	}
	cpuTopol, cpu, extDevs := topology.NewDevsPoller(numaWeight)

	socketFile := fmt.Sprintf("cph-cpudp_%s.sock", poolName)
	cdm := &cpuDeviceManager{
		pool:               pool,
		socketFile:         socketFile,
		sharedPoolCPUs:     sharedCPUs,
		poolType:           types.DeterminePoolType(poolName),
		nodeTopology:       topology.GetNodeTopology(),
		htTopology:         topology.GetHTTopology(),
		numCPU:             int(utils.GetCpuInfo().Cores),
		cloudphoneExtDevs:  extDevs,
		cloudphoneCPU:      cpu,
		cloudphoneCPUTopol: cpuTopol,
		devicePluginPath:   "/var/lib/kubelet/device-plugins/",
	}
	return cdm
}

func validatePools(poolConf types.PoolConfig) (string, error) {
	var sharedCPUs string
	var err error
	for poolName, pool := range poolConf.Pools {
		poolType := types.DeterminePoolType(poolName)
		if poolType == types.SharedPoolID {
			if sharedCPUs != "" {
				err = fmt.Errorf("Only one shared pool allowed")
				glog.Errorf("Pool config : %v", poolConf)
				break
			}
			sharedCPUs = pool.CPUset.String()
		}
	}
	return sharedCPUs, err
}

func createCDMs(poolConf types.PoolConfig, sharedCPUs string) error {
	var err error
	for poolName, pool := range poolConf.Pools {
		poolType := types.DeterminePoolType(poolName)
		//Deault or unrecognizable pools need not be made available to Device Manager as schedulable devices
		if poolType == types.DefaultPoolID {
			continue
		}
		cdm := newCPUDeviceManager(poolName, pool, sharedCPUs)
		cdms = append(cdms, cdm)
		if err := cdm.Start(); err != nil {
			glog.Errorf("cpuDeviceManager.Start() failed: %v", err)
			break
		}
		resourceName := resourceBaseName + "/" + poolName
		err := cdm.Register(path.Join(pluginapi.DevicePluginPath, "kubelet.sock"), resourceName)
		if err != nil {
			// Stop server
			cdm.grpcServer.Stop()
			glog.Error(err)
			break
		}
		glog.Infof("CPU device plugin registered with the Kubelet")
	}
	return err
}

func createPluginsForPools() error {
	files, err := filepath.Glob(filepath.Join(pluginapi.DevicePluginPath, "cpudp*"))
	if err != nil {
		glog.Fatal(err)
	}
	for _, f := range files {
		if err := os.Remove(f); err != nil {
			glog.Fatal(err)
		}
	}

	// 找不到匹配的配置在这里退出
	poolConf, err := types.DeterminePoolConfig()
	if err != nil {
		glog.Fatal(err)
	}
	glog.Infof("Pool configuration %v", poolConf)

	var sharedCPUs string
	sharedCPUs, err = validatePools(poolConf)
	if err != nil {
		return err
	}

	if err := createCDMs(poolConf, sharedCPUs); err != nil {
		for _, cdm := range cdms {
			cdm.Stop()
		}
	}
	return err
}

func main() {
	flag.Parse()
	// 监听 kubelet.sock ，如果 kubelet.sock 有变更事件，则重启 device-plugin 注册
	watcher, _ := fsnotify.NewWatcher()
	watcher.Add(path.Join(pluginapi.DevicePluginPath, "kubelet.sock"))
	defer watcher.Close()

	// respond to syscalls for termination
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	if err := createPluginsForPools(); err != nil {
		glog.Fatalf("Failed to start device plugin: %v", err)
	}

	/* Monitor file changes for kubelet socket file and termination signals */
	for {
		select {
		case sig := <-sigCh:
			switch sig {
			case syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGINT:
				glog.Infof("Received signal \"%v\", shutting down.", sig)
				for _, cdm := range cdms {
					cdm.Stop()
				}
				return
			}
			glog.Infof("Received signal \"%v\"", sig)

		case event := <-watcher.Events:
			glog.Infof("Kubelet change event in pluginpath %v", event)
			for _, cdm := range cdms {
				cdm.Stop()
			}
			cdms = nil
			if err := createPluginsForPools(); err != nil {
				panic("Failed to restart device plugin")
			}
		}
	}
}
