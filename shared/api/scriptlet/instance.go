package scriptlet

// InstanceResources represents the required resources for an instance.
//
// API extension: instances_placement_scriptlet.
type InstanceResources struct {
	CPUCores     uint64 `json:"cpu_cores"`
	MemorySize   uint64 `json:"memory_size"`
	RootDiskSize uint64 `json:"root_disk_size"`
}
