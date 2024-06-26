package main

import (
	"flag"
	"github.com/nokia/CPU-Pooler/pkg/sethandler"
	"github.com/nokia/CPU-Pooler/pkg/types"
	"log"
	"os"
	"os/signal"
	"syscall"
)

const (
	//NumberOfWorkers controls how many asynch event handler threads are started in the CPUSetter controller
	NumberOfWorkers = 100
)

var (
	kubeConfig     string
	poolConfigPath string
	cpusetRoot     string
	cpuRoot        string
)

func main() {
	flag.Parse()
	if poolConfigPath == "" || cpusetRoot == "" {
		log.Fatal("ERROR: Mandatory command-line arguments poolconfigs and cpusetroot were not provided!")
	}
	poolConf, err := types.DeterminePoolConfig()
	if err != nil {
		log.Fatal("ERROR: Could not read CPU pool configuration files because: " + err.Error() + ", exiting!")
	}
	setHandler, err := sethandler.New(kubeConfig, poolConf, cpusetRoot, cpuRoot)
	if err != nil {
		log.Fatal("ERROR: Could not initalize K8s client because of error: " + err.Error() + ", exiting!")
	}

	stopChannel := make(chan struct{})
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)
	log.Println("CPUSetter's Controller initalized successfully!")
	setHandler.Run(NumberOfWorkers, &stopChannel)
	select {
	case <-signalChannel:
		log.Println("Orchestrator initiated graceful shutdown, ending CPUSetter workers...(o_o)/")
		setHandler.Stop()
	}
}

func init() {
	flag.StringVar(&poolConfigPath, "poolconfigs", "", "Path to the pool configuration files. Mandatory parameter.")
	flag.StringVar(&cpusetRoot, "cpusetroot", "", "The root of the cgroupfs where Kubernetes creates the cpusets for the Pods . Mandatory parameter.")
	flag.StringVar(&cpuRoot, "cpuroot", "", "The root of the system where Kubernetes creates the cpu for the Pods . Mandatory parameter.")
	flag.StringVar(&kubeConfig, "kubeconfig", "", "Path to a kubeconfig. Optional parameter, only required if out-of-cluster.")
}
