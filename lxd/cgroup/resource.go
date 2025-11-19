package cgroup

// resourceMap associates each [Resource] with controller keys to check for support.
var resourceMap = map[Resource][]string{
	Blkio:              {"blkio", "io"},
	BlkioWeight:        {"blkio.weight", "io"},
	CPU:                {"cpu"},
	CPUAcct:            {"cpuacct", "cpu"},
	CPUSet:             {"cpuset"},
	Devices:            {"devices"},
	Freezer:            {"freezer"},
	Hugetlb:            {"hugetlb"},
	Memory:             {"memory"},
	MemoryMaxUsage:     {"memory.max_usage_in_bytes"},
	MemorySwap:         {"memory.memsw.limit_in_bytes", "memory.swap.max"},
	MemorySwapMaxUsage: {"memory.memsw.max_usage_in_bytes"},
	MemorySwapUsage:    {"memory.memsw.usage_in_bytes", "memory.swap.current"},
	MemorySwappiness:   {"memory.swappiness"},
	NetPrio:            {"net_prio"},
	Pids:               {"pids"},
}

// supportsVersion indicates whether or not a resource is controllable and in which type of cgroup filesystem.
func supportsVersion(keys []string) (Backend, bool) {
	for _, key := range keys {
		val, ok := cgControllers[key]
		if ok {
			return val, ok
		}
	}

	return Unavailable, false
}
