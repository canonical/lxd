package cgroup

// ResourceInterface defines methods to query cgroup resource support.
type ResourceInterface interface {
	SupportsVersion() (Backend, bool)
}

// BlkioResource implements ResourceInterface for the blkio controller.
type BlkioResource struct{}

// SupportsVersion indicates whether or not the blkio controller is controllable and in which type of cgroup filesystem.
func (blkio BlkioResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["blkio"]
	if ok {
		return val, ok
	}

	val, ok = cgControllers["io"]
	if ok {
		return val, ok
	}

	return Unavailable, false
}

// BlkioWeightResource implements ResourceInterface for the blkio weight controller.
type BlkioWeightResource struct{}

// SupportsVersion indicates whether or not the blkio weight controller is controllable and in which type of cgroup filesystem.
func (blkioWeight BlkioWeightResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["blkio.weight"]
	if ok {
		return val, ok
	}

	val, ok = cgControllers["io"]
	if ok {
		return val, ok
	}

	return Unavailable, false
}

// CPUResource implements ResourceInterface for the cpu controller.
type CPUResource struct{}

// SupportsVersion indicates whether or not the cpu controller is controllable and in which type of cgroup filesystem.
func (cpu CPUResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["cpu"]
	if !ok {
		return Unavailable, false
	}

	return val, true
}

// CPUAcctResource implements ResourceInterface for the cpuacct controller.
type CPUAcctResource struct{}

// SupportsVersion indicates whether or not the cpuacct controller is controllable and in which type of cgroup filesystem.
func (cpuacct CPUAcctResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["cpuacct"]
	if ok {
		return val, ok
	}

	val, ok = cgControllers["cpu"]
	if ok {
		return val, ok
	}

	return Unavailable, false
}

// CPUSetResource implements ResourceInterface for the cpuset controller.
type CPUSetResource struct{}

// SupportsVersion indicates whether or not the cpuset controller is controllable and in which type of cgroup filesystem.
func (cpuset CPUSetResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["cpuset"]
	if !ok {
		return Unavailable, false
	}

	return val, true
}

// DevicesResource implements ResourceInterface for the devices controller.
type DevicesResource struct{}

// SupportsVersion indicates whether or not the devices controller is controllable and in which type of cgroup filesystem.
func (devices DevicesResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["devices"]
	if !ok {
		return Unavailable, false
	}

	return val, true
}

// FreezerResource implements ResourceInterface for the freezer controller.
type FreezerResource struct{}

// SupportsVersion indicates whether or not the freezer controller is controllable and in which type of cgroup filesystem.
func (freezer FreezerResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["freezer"]
	if !ok {
		return Unavailable, false
	}

	return val, true
}

// HugetlbResource implements ResourceInterface for the hugetlb controller.
type HugetlbResource struct{}

// SupportsVersion indicates whether or not the hugetlb controller is controllable and in which type of cgroup filesystem.
func (hugetlb HugetlbResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["hugetlb"]
	if !ok {
		return Unavailable, false
	}

	return val, true
}

// MemoryResource implements ResourceInterface for the memory controller.
type MemoryResource struct{}

// SupportsVersion indicates whether or not the memory controller is controllable and in which type of cgroup filesystem.
func (memory MemoryResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["memory"]
	if !ok {
		return Unavailable, false
	}

	return val, true
}

// MemoryMaxUsageResource implements ResourceInterface for the memory max usage controller.
type MemoryMaxUsageResource struct{}

// SupportsVersion indicates whether or not the memory max usage controller is controllable and in which type of cgroup filesystem.
func (memoryMaxUsage MemoryMaxUsageResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["memory.max_usage_in_bytes"]
	if !ok {
		return Unavailable, false
	}

	return val, true
}

// MemorySwapResource implements ResourceInterface for the memory swap controller.
type MemorySwapResource struct{}

// SupportsVersion indicates whether or not the memory swap controller is controllable and in which type of cgroup filesystem.
func (memorySwap MemorySwapResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["memory.memsw.limit_in_bytes"]
	if ok {
		return val, ok
	}

	val, ok = cgControllers["memory.swap.max"]
	if ok {
		return val, ok
	}

	return Unavailable, false
}

// MemorySwapMaxUsageResource implements ResourceInterface for the memory swap max usage controller.
type MemorySwapMaxUsageResource struct{}

// SupportsVersion indicates whether or not the memory swap max usage controller is controllable and in which type of cgroup filesystem.
func (memorySwapMaxUsage MemorySwapMaxUsageResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["memory.memsw.max_usage_in_bytes"]
	if ok {
		return val, ok
	}

	return Unavailable, false
}

// MemorySwapUsageResource implements ResourceInterface for the memory swap usage controller.
type MemorySwapUsageResource struct{}

// SupportsVersion indicates whether or not the memory swap usage controller is controllable and in which type of cgroup filesystem.
func (memorySwapUsage MemorySwapUsageResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["memory.memsw.usage_in_bytes"]
	if ok {
		return val, ok
	}

	val, ok = cgControllers["memory.swap.current"]
	if ok {
		return val, ok
	}

	return Unavailable, false
}

// MemorySwappinessResource implements ResourceInterface for the memory swappiness controller.
type MemorySwappinessResource struct{}

// SupportsVersion indicates whether or not the memory swappiness controller is controllable and in which type of cgroup filesystem.
func (memorySwappiness MemorySwappinessResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["memory.swappiness"]
	if ok {
		return val, ok
	}

	return Unavailable, false
}

// NetPrioResource implements ResourceInterface for the net_prio controller.
type NetPrioResource struct{}

// SupportsVersion indicates whether or not the net_prio controller is controllable and in which type of cgroup filesystem.
func (netPrio NetPrioResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["net_prio"]
	if !ok {
		return Unavailable, false
	}

	return val, true
}

// PidsResource implements ResourceInterface for the pids controller.
type PidsResource struct{}

// SupportsVersion indicates whether or not the pids controller is controllable and in which type of cgroup filesystem.
func (pids PidsResource) SupportsVersion() (Backend, bool) {
	val, ok := cgControllers["pids"]
	if !ok {
		return Unavailable, false
	}

	return val, true
}

// resourceMap associates each Resource with the ResourceInterface implementation that checks its support.
var resourceMap = map[Resource]ResourceInterface{
	Blkio:              BlkioResource{},
	BlkioWeight:        BlkioWeightResource{},
	CPU:                CPUResource{},
	CPUAcct:            CPUAcctResource{},
	CPUSet:             CPUSetResource{},
	Devices:            DevicesResource{},
	Freezer:            FreezerResource{},
	Hugetlb:            HugetlbResource{},
	Memory:             MemoryResource{},
	MemoryMaxUsage:     MemoryMaxUsageResource{},
	MemorySwap:         MemorySwapResource{},
	MemorySwapMaxUsage: MemorySwapMaxUsageResource{},
	MemorySwapUsage:    MemorySwapUsageResource{},
	MemorySwappiness:   MemorySwappinessResource{},
	NetPrio:            NetPrioResource{},
	Pids:               PidsResource{},
}
