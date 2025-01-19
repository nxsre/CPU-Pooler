package types

const (
	CheckPointFileName = "kubelet_internal_checkpoint"
)

type DevicesPerNUMA map[int64][]string

type PodDevicesEntry struct {
	PodUID        string
	ContainerName string
	ResourceName  string
	DeviceIDs     []string
	AllocResp     []byte
}

type PodDevicesEntryNUMA struct {
	PodUID        string
	ContainerName string
	ResourceName  string
	DeviceIDs     DevicesPerNUMA
	AllocResp     []byte
}

type CheckpointNUMA struct {
	PodDeviceEntries  []PodDevicesEntryNUMA
	RegisteredDevices map[string][]string
}

type Checkpoint struct {
	PodDeviceEntries  []PodDevicesEntry
	RegisteredDevices map[string][]string
}

type CheckpointDataNUMA struct {
	Data *CheckpointNUMA `json:"Data"`
}

type CheckpointData struct {
	Data *Checkpoint `json:"Data"`
}
