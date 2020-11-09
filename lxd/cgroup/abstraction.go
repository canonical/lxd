package cgroup

import (
	"fmt"
	"strconv"
)

// CGroup represents the main cgroup abstraction.
type CGroup struct {
	rw             ReadWriter
	UnifiedCapable bool
}

// SetMaxProcesses applies a limit to the number of processes
func (cg *CGroup) SetMaxProcesses(limit int64) error {
	version := cgControllers["pids"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		fallthrough
	case V2:
		if limit == -1 {
			return cg.rw.Set(version, "pids", "pids.max", "max")
		}

		return cg.rw.Set(version, "pids", "pids.max", fmt.Sprintf("%d", limit))
	}

	return ErrUnknownVersion
}

// GetMemorySoftLimit returns the soft limit for memory
func (cg *CGroup) GetMemorySoftLimit() (int64, error) {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		val, err := cg.rw.Get(version, "memory", "memory.soft_limit_in_bytes")
		if err != nil {
			return -1, err
		}

		return strconv.ParseInt(val, 10, 64)
	case V2:
		val, err := cg.rw.Get(version, "memory", "memory.low")
		if err != nil {
			return -1, err
		}

		return strconv.ParseInt(val, 10, 64)
	}

	return -1, ErrUnknownVersion
}

// SetMemorySoftLimit set the soft limit for memory
func (cg *CGroup) SetMemorySoftLimit(limit int64) error {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "memory", "memory.soft_limit_in_bytes", fmt.Sprintf("%d", limit))
	case V2:
		if limit == -1 {
			return cg.rw.Set(version, "memory", "memory.low", "max")
		}

		return cg.rw.Set(version, "memory", "memory.low", fmt.Sprintf("%d", limit))
	}

	return ErrUnknownVersion
}

// GetMemoryLimit return the hard limit for memory
func (cg *CGroup) GetMemoryLimit() (int64, error) {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		val, err := cg.rw.Get(version, "memory", "memory.limit_in_bytes")
		if err != nil {
			return -1, err
		}

		return strconv.ParseInt(val, 10, 64)
	case V2:
		val, err := cg.rw.Get(version, "memory", "memory.max")
		if err != nil {
			return -1, err
		}

		return strconv.ParseInt(val, 10, 64)
	}

	return -1, ErrUnknownVersion
}

// SetMemoryLimit sets the hard limit for memory
func (cg *CGroup) SetMemoryLimit(limit int64) error {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "memory", "memory.limit_in_bytes", fmt.Sprintf("%d", limit))
	case V2:
		if limit == -1 {
			return cg.rw.Set(version, "memory", "memory.max", "max")
		}

		return cg.rw.Set(version, "memory", "memory.max", fmt.Sprintf("%d", limit))
	}

	return ErrUnknownVersion
}

// GetMemoryUsage returns the current use of memory
func (cg *CGroup) GetMemoryUsage() (int64, error) {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		val, err := cg.rw.Get(version, "memory", "memory.usage_in_bytes")
		if err != nil {
			return -1, err
		}

		return strconv.ParseInt(val, 10, 64)
	case V2:
		val, err := cg.rw.Get(version, "memory", "memory.current")
		if err != nil {
			return -1, err
		}

		return strconv.ParseInt(val, 10, 64)
	}

	return -1, ErrUnknownVersion
}

// GetProcessesUsage returns the current number of pids
func (cg *CGroup) GetProcessesUsage() (int64, error) {
	version := cgControllers["pids"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		fallthrough
	case V2:
		val, err := cg.rw.Get(version, "pids", "pids.current")
		if err != nil {
			return -1, err
		}

		return strconv.ParseInt(val, 10, 64)
	}

	return -1, ErrUnknownVersion
}

// SetMemorySwapLimit sets the hard limit for swap
func (cg *CGroup) SetMemorySwapLimit(limit int64) error {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		val, err := cg.rw.Get(version, "memory", "memory.limit_in_bytes")
		if err != nil {
			return err
		}

		valInt, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return err
		}

		return cg.rw.Set(version, "memory", "memory.memsw.limit_in_bytes", fmt.Sprintf("%d", limit+valInt))
	case V2:
		if limit == -1 {
			return cg.rw.Set(version, "memory", "memory.swap.max", "max")
		}

		return cg.rw.Set(version, "memory", "memory.swap.max", fmt.Sprintf("%d", limit))
	}

	return ErrUnknownVersion
}

// GetCPUAcctUsage returns the total CPU time in ns used by processes
func (cg *CGroup) GetCPUAcctUsage() (int64, error) {
	version := cgControllers["cpuacct"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		val, err := cg.rw.Get(version, "cpuacct", "cpuacct.usage")
		if err != nil {
			return -1, err
		}

		return strconv.ParseInt(val, 10, 64)
	case V2:
		return -1, ErrControllerMissing
	}

	return -1, ErrUnknownVersion
}

// GetMemoryMaxUsage returns the record high for memory usage
func (cg *CGroup) GetMemoryMaxUsage() (int64, error) {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		val, err := cg.rw.Get(version, "memory", "memory.max_usage_in_bytes")
		if err != nil {
			return -1, err
		}

		return strconv.ParseInt(val, 10, 64)
	case V2:
		return -1, ErrControllerMissing
	}

	return -1, ErrUnknownVersion
}

// GetMemorySwapMaxUsage returns the record high for swap usage
func (cg *CGroup) GetMemorySwapMaxUsage() (int64, error) {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		swapVal, err := cg.rw.Get(version, "memory", "memory.memsw.max_usage_in_bytes")
		if err != nil {
			return -1, err
		}

		swapValInt, err := strconv.ParseInt(swapVal, 10, 64)
		if err != nil {
			return -1, err
		}

		memVal, err := cg.rw.Get(version, "memory", "memory.max_usage_in_bytes")
		if err != nil {
			return -1, err
		}

		memValInt, err := strconv.ParseInt(memVal, 10, 64)
		if err != nil {
			return -1, err
		}

		return swapValInt - memValInt, nil
	case V2:
		return -1, ErrControllerMissing
	}

	return -1, ErrUnknownVersion
}

// SetMemorySwappiness sets swappiness paramet of vmscan
func (cg *CGroup) SetMemorySwappiness(limit int64) error {
	// Confirm we have the controller
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "memory", "memory.swappiness", fmt.Sprintf("%d", limit))
	case V2:
		return ErrControllerMissing
	}

	return ErrUnknownVersion
}

// GetMemorySwapLimit returns the hard limit on swap usage
func (cg *CGroup) GetMemorySwapLimit() (int64, error) {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		swapVal, err := cg.rw.Get(version, "memory", "memory.memsw.limit_in_bytes")
		if err != nil {
			return -1, err
		}

		swapValInt, err := strconv.ParseInt(swapVal, 10, 64)
		if err != nil {
			return -1, err
		}

		memVal, err := cg.rw.Get(version, "memory", "memory.limit_in_bytes")
		if err != nil {
			return -1, err
		}

		memValInt, err := strconv.ParseInt(memVal, 10, 64)
		if err != nil {
			return -1, err
		}

		return swapValInt - memValInt, nil
	case V2:
		val, err := cg.rw.Get(version, "memory", "memory.swap.max")
		if err != nil {
			return -1, err
		}

		return strconv.ParseInt(val, 10, 64)
	}
	return -1, ErrUnknownVersion
}

// GetMemorySwapUsage return current usage of swap
func (cg *CGroup) GetMemorySwapUsage() (int64, error) {
	version := cgControllers["memory"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		swapVal, err := cg.rw.Get(version, "memory", "memory.memsw.usage_in_bytes")
		if err != nil {
			return -1, err
		}

		swapValInt, err := strconv.ParseInt(swapVal, 10, 64)
		if err != nil {
			return -1, err
		}

		memVal, err := cg.rw.Get(version, "memory", "memory.usage_in_bytes")
		if err != nil {
			return -1, err
		}

		memValInt, err := strconv.ParseInt(memVal, 10, 64)
		if err != nil {
			return -1, err
		}

		return swapValInt - memValInt, nil
	case V2:
		val, err := cg.rw.Get(version, "memory", "memory.swap.current")
		if err != nil {
			return -1, err
		}

		return strconv.ParseInt(val, 10, 64)
	}

	return -1, ErrUnknownVersion
}

// GetBlkioWeight returns the currently allowed range of weights
func (cg *CGroup) GetBlkioWeight() (int64, error) {
	version := cgControllers["blkio"]
	switch version {
	case Unavailable:
		return -1, ErrControllerMissing
	case V1:
		val, err := cg.rw.Get(version, "blkio", "blkio.weight")
		if err != nil {
			return -1, err
		}

		return strconv.ParseInt(val, 10, 64)
	case V2:
		val, err := cg.rw.Get(version, "io", "io.weight")
		if err != nil {
			return -1, err
		}

		return strconv.ParseInt(val, 10, 64)
	}

	return -1, ErrUnknownVersion
}

// SetBlkioWeight sets the currently allowed range of weights
func (cg *CGroup) SetBlkioWeight(limit int64) error {
	version := cgControllers["blkio"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "blkio", "blkio.weight", fmt.Sprintf("%d", limit))
	case V2:
		return cg.rw.Set(version, "io", "io.weight", fmt.Sprintf("%d", limit))
	}

	return ErrUnknownVersion
}

// SetCPUShare sets the weight of each group in the same hierarchy
func (cg *CGroup) SetCPUShare(limit int64) error {
	version := cgControllers["cpu"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "cpu", "cpu.shares", fmt.Sprintf("%d", limit))
	case V2:
		return ErrControllerMissing
	}

	return ErrUnknownVersion
}

// SetCPUCfsPeriod sets the duration in ms for each scheduling period
func (cg *CGroup) SetCPUCfsPeriod(limit int64) error {
	version := cgControllers["cpu"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "cpu", "cpu.cfs_period_us", fmt.Sprintf("%d", limit))
	case V2:
		return ErrControllerMissing
	}

	return ErrUnknownVersion
}

// SetCPUCfsQuota sets the max time in ms during each cfs_period_us that
// the current group can run for
func (cg *CGroup) SetCPUCfsQuota(limit int64) error {
	version := cgControllers["cpu"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "cpu", "cpu.cfs_quota_us", fmt.Sprintf("%d", limit))
	case V2:
		return ErrControllerMissing
	}

	return ErrUnknownVersion
}

// SetNetIfPrio sets the priority for the process
func (cg *CGroup) SetNetIfPrio(limit string) error {
	version := cgControllers["net_prio"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "net_prio", "net_prio.ifpriomap", limit)
	case V2:
		return ErrControllerMissing
	}

	return ErrUnknownVersion
}

// SetHugepagesLimit applies a limit to the number of processes
func (cg *CGroup) SetHugepagesLimit(pageType string, limit int64) error {
	version := cgControllers["hugetlb"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "hugetlb", fmt.Sprintf("hugetlb.%s.limit_in_bytes", pageType), fmt.Sprintf("%d", limit))
	case V2:
		if limit == -1 {
			return cg.rw.Set(version, "hugetlb", fmt.Sprintf("hugetlb.%s.max", pageType), "max")
		}
		return cg.rw.Set(version, "hugetlb", fmt.Sprintf("hugetlb.%s.max", pageType), fmt.Sprintf("%d", limit))
	}

	return ErrUnknownVersion
}

// GetEffectiveCpuset returns the current set of CPUs for the cgroup
func (cg *CGroup) GetEffectiveCpuset() (string, error) {
	version := cgControllers["cpuset"]
	switch version {
	case Unavailable:
		return "", ErrControllerMissing
	case V1:
		return cg.rw.Get(version, "cpuset", "cpuset.effective_cpus")
	case V2:
		return cg.rw.Get(version, "cpuset", "cpuset.cpus.effective")
	}

	return "", ErrUnknownVersion
}

// GetCpuset returns the current set of CPUs for the cgroup
func (cg *CGroup) GetCpuset() (string, error) {
	version := cgControllers["cpuset"]
	switch version {
	case Unavailable:
		return "", ErrControllerMissing
	case V1:
		return cg.rw.Get(version, "cpuset", "cpuset.cpus")
	case V2:
		return cg.rw.Get(version, "cpuset", "cpuset.cpus")
	}

	return "", ErrUnknownVersion
}

// SetCpuset set the currently allowed set of CPUs for the cgroups
func (cg *CGroup) SetCpuset(limit string) error {
	version := cgControllers["cpuset"]
	switch version {
	case Unavailable:
		return ErrControllerMissing
	case V1:
		return cg.rw.Set(version, "cpuset", "cpuset.cpus", limit)
	case V2:
		return cg.rw.Set(version, "cpuset", "cpuset.cpus", limit)
	}

	return ErrUnknownVersion
}
