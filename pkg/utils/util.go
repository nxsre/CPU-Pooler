package utils

import (
	"context"
	"encoding/json"
	"github.com/nokia/CPU-Pooler/pkg/types"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"
	"net"
	"os"
	"path/filepath"
	"time"
)

var (
	//DefaultDialOptions contains default dial options used in grpc dial
	DefaultDialOptions = []grpc.DialOption{grpc.WithInsecure(), grpc.WithDialer(UnixDial), grpc.WithBlock()}
)

// UnixDial dials to a unix socket using net.DialTimeout
func UnixDial(addr string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", addr, timeout)
}

// WaitForServer checks if grpc server is alive
// by making grpc blocking connection to the server socket
func WaitForServer(socket string) error {
	conn, err := grpc.DialContext(context.Background(), socket, DefaultDialOptions...)
	if err == nil {
		conn.Close()
		return nil
	}
	return errors.Wrapf(err, "Failed dial context at %s", socket)
}

func GetCheckpointData(devicePluginPath string) (*types.Checkpoint, error) {
	cpFile := filepath.Join(devicePluginPath, types.CheckPointFileName)
	data, err := os.ReadFile(cpFile)
	if err != nil {
		return nil, err
	}
	klog.V(4).Infof("Try NUMA checkpoint data format")
	cpNUMAData := &types.CheckpointDataNUMA{}
	err = json.Unmarshal(data, cpNUMAData)
	if err != nil {
		klog.V(4).Infof("Failed NUMA checkpoint data format")
	} else { // flat deviceids
		v2DeivcesEntryies := make([]types.PodDevicesEntry, len(cpNUMAData.Data.PodDeviceEntries))
		for i, v := range cpNUMAData.Data.PodDeviceEntries {
			v2PodDevicesEntry := types.PodDevicesEntry{
				PodUID:        v.PodUID,
				ContainerName: v.ContainerName,
				ResourceName:  v.ResourceName,
				DeviceIDs:     make([]string, 0),
				AllocResp:     v.AllocResp,
			}
			for _, devices := range v.DeviceIDs {
				v2PodDevicesEntry.DeviceIDs = append(v2PodDevicesEntry.DeviceIDs, devices...)
			}
			v2DeivcesEntryies[i] = v2PodDevicesEntry
		}
		cpV1Data := &types.Checkpoint{}
		cpV1Data.RegisteredDevices = cpNUMAData.Data.RegisteredDevices
		cpV1Data.PodDeviceEntries = v2DeivcesEntryies
		return cpV1Data, nil
	}

	klog.V(4).Infof("Try v2 checkpoint data format")
	cpV2Data := &types.CheckpointData{}
	err = json.Unmarshal(data, cpV2Data)
	if err != nil {
		return nil, err
	}

	if cpV2Data.Data != nil {
		return cpV2Data.Data, nil
	}

	klog.V(4).Infof("Try v1 checkpoint data format")
	cpV1Data := &types.Checkpoint{}
	err = json.Unmarshal(data, cpV1Data)
	if err != nil {
		return nil, err
	}

	return cpV1Data, nil
}
