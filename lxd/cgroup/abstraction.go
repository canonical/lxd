package cgroup

import (
	"fmt"
)

// CGroup represents the main cgroup abstraction.
type CGroup struct {
	rw ReadWriter
}

// SetMaxProcesses applies a limit to the number of processes
func (cg *CGroup) SetMaxProcesses(max int64) error {
	// Confirm we have the controller
	version := cgControllers["pids"]
	if version == Unavailable {
		return ErrControllerMissing
	}

	// V1/V2 behavior
	if version == V1 || version == V2 {
		// Setting pids limits is conveniently identical on V1 and V2.
		if max == -1 {
			return cg.rw.Set(version, "pids", "pids.max", "max")
		}

		return cg.rw.Set(version, "pids", "pids.max", fmt.Sprintf("%d", max))
	}

	return ErrUnknownVersion
}

func (cg *CGroup) GetMaxProcess() (string, error) {
	version := cgControllers["pids"]
	if version == Unavailable {
		return "", ErrControllerMissing
	}
	if version == V1 || version == V2 {
		return cg.rw.Get(version, "pids", "pids.max")
	}
	return "", ErrUnknownVersion
}

func (cg *CGroup) GetMemorySoftLimit() (string, error) {
	version := cgControllers["memory"]
	if version == Unavailable {
		return "", ErrControllerMissing
	}
	if version == V1 {
		return cg.rw.Get(version, "memory", "memory.soft_limit_in_bytes")
	}
	if version == V2 {
		return cg.rw.Get(version, "memory", "memory.low")
	}
	return "", ErrUnknownVersion
}

func (cg *CGroup) SetMemorySoftLimit(softLim string) error {
	// Confirm we have the controller
	version := cgControllers["memory"]
	if version == Unavailable {
		return ErrControllerMissing
	}
	// V1/V2 behavior
	if version == V1 {
		if  softLim == "-1" {
			return cg.rw.Set(version, "memory", "memory.soft_limit_in_bytes", "max")
		}
		return cg.rw.Set(version, "memory", "memory.soft_limit_in_bytes",softLim)
	}
	if version == V2 {
		if  softLim == "-1" {
			return cg.rw.Set(version, "memory", "memory.low", "max")
		}
		return cg.rw.Set(version, "memory","memory.low", softLim)
	}

	return ErrUnknownVersion
}


func (cg *CGroup) GetMaxMemory() (string, error) {
	version := cgControllers["memory"]
	if version == Unavailable {
		return "", ErrControllerMissing
	}
	if version == V1 {
		return cg.rw.Get(version, "memory", "memory.limit_in_bytes")
	}
	if version == V2 {
		return cg.rw.Get(version, "memory", "memory.max")
	}
	return "", ErrUnknownVersion
}
func (cg *CGroup) SetMemoryMax(max string) error {
	// Confirm we have the controller
	version := cgControllers["memory"]
	if version == Unavailable {
		return ErrControllerMissing
	}
	// V1/V2 behavior
	if version == V1 {
		return cg.rw.Set(version, "memory", "memory.limit_in_bytes",max)
	}
	if version == V2 {
		return cg.rw.Set(version, "memory","memory.max", max)
	}
	return ErrUnknownVersion
}

func (cg *CGroup) GetMemoryUsage() (string, error) {
	version := cgControllers["memory"]
	if version == Unavailable {
		return "", ErrControllerMissing
	}
	if version == V1 {
		return cg.rw.Get(version, "memory", "memory.usage_in_bytes")
	}
	if version == V2 {
		return cg.rw.Get(version, "memory", "memory.current")
	}
	return "", ErrUnknownVersion
}

func (cg *CGroup) GetProcessesUsage() (string, error) {
	version := cgControllers["pids"]
	if version == Unavailable {
		return "", ErrControllerMissing
	}
	if version == V1 || version == V2 {
		return cg.rw.Get(version, "pids", "pids.current")
	}

	return "", ErrUnknownVersion
}

func (cg *CGroup) SetMemorySwapMax(max string) error {
	//Confirm we have the controller
	version := cgControllers["memory"]
	if version == Unavailable {
		return ErrControllerMissing
	}
	// V1/V2 behavior
	if version == V1 {
		if max == "-1" {
			return cg.rw.Set(version, "memory","memory.memsw.limit_in_bytes", "max")
		}

		return cg.rw.Set(version, "memory","memory.memsw.limit_in_bytes", max)
	}
	if version == V2 {
		if max == "-1" {
			return cg.rw.Set(version, "memory","memory.swap.max", "max")

		}

		return cg.rw.Set(version, "memory","memory.swap.max", max)
	}
	return ErrUnknownVersion
}
func (cg *CGroup) GetCpuAcctUsage() (string, error) {
	version := cgControllers["cpuacct"]
	//only supported in V1 currently
	if version == Unavailable || version == V2 {
		return "", ErrControllerMissing
	}
	if version == V1 {
		return cg.rw.Get(version, "cpuacct", "cpuacct.usage")
	}

	return "", ErrUnknownVersion
}

func (cg *CGroup) GetMemoryMaxUsage() (string, error) {
	version := cgControllers["memory"]
	//only supported in V1 currently
	if version == Unavailable || version == V2 {
		return "", ErrControllerMissing
	}
	if version == V1 {
		return cg.rw.Get(version, "memory", "memory.max_usage_in_bytes")
	}

	return "", ErrUnknownVersion
}

func (cg *CGroup) GetMemorySwMaxUsage() (string, error) {
	version := cgControllers["memory"]
	//only supported in V1 currently
	if version == Unavailable || version == V2 {
		return "", ErrControllerMissing
	}
	if version == V1 {
		return cg.rw.Get(version, "memory", "memory.memsw.max_usage_in_bytes")
	}
	return "", ErrUnknownVersion
}

func (cg *CGroup) SetMemorySwappiness(value string) error {
	// Confirm we have the controller
	version := cgControllers["memory"]
	if version == Unavailable || version == V2{
		return ErrControllerMissing
	}
	// V1 behavior
	if version == V1 {
		return cg.rw.Set(version, "memory","memory.swappiness", value)
	}
	return ErrUnknownVersion
}


func (cg *CGroup) GetMemorySwapLimit() (string, error) {
	version := cgControllers["memory"]
	if version == Unavailable   {
		return "", ErrControllerMissing
	}
	if version == V1 {
		return cg.rw.Get(version, "memory", "memory.memsw.limit_in_bytes")
	}
	if version == V2 {
		return cg.rw.Get(version, "memory", "memory.swap.max")
	}
	return "", ErrUnknownVersion
}

func (cg *CGroup) GetMemorySwapUsage() (string, error) {
	version := cgControllers["memory"]
	//only supported in V1 currently
	if version == Unavailable   {
		return "", ErrControllerMissing
	}
	if version == V1 {
		return cg.rw.Get(version, "memory", "memory.memsw.usage_in_bytes")
	}
	if version == V2 {
		return cg.rw.Get(version, "memory", "memory.swap.current")
	}
	return "", ErrUnknownVersion
}

func (cg *CGroup) GetBlkioWeight() (string, error) {
	// Confirm we have the controller
	version := cgControllers["blkio"]
	if version == Unavailable  || version == V2 {
		return "", ErrControllerMissing
	}
	// V1/V2 behavior
	if version == V1 {
		return cg.rw.Get(version, "blkio", "blkio.weight")
		}

	return "", ErrUnknownVersion
}

func (cg *CGroup) SetCpusShare(value string) error {
	//Confirm we have the controller
	version := cgControllers["cpu"]
	if version == Unavailable {
		return ErrControllerMissing
	}
	// V1/V2 behavior
	if version == V1 {
		return cg.rw.Set(version, "cpu","cpu.shares", value)
	}
	if version == V2 {
		return cg.rw.Set(version, "cpu","cpu.weight", value)
	}
	return ErrUnknownVersion
}

func (cg *CGroup) SetCpuCfsPeriod(value string) error {
	//Confirm we have the controller
	version := cgControllers["cpu"]
	if version == Unavailable || version == V2 {
		return ErrControllerMissing
	}
	// V1 behavior
	if version == V1 {
		return cg.rw.Set(version, "cpu","cpu.cfs_period_us", value)
	}

	return ErrUnknownVersion
}

func (cg *CGroup) SetCpuCfsQuota(value string) error {
	//Confirm we have the controller
	version := cgControllers["cpu"]
	if version == Unavailable || version == V2 {
		return ErrControllerMissing
	}
	// V1/V2 behavior
	if version == V1 {
		return cg.rw.Set(version, "cpu","cpu.cfs_quota_us", value)
	}
	return ErrUnknownVersion
}

func (cg *CGroup) SetNetIfPrio(value string) error {
	version := cgControllers["net_prio"]
	if version == Unavailable || version == V2 {
		return ErrControllerMissing
	}
	// V1 behavior
	if version == V1 {
		return cg.rw.Set(version, "net_prio","net_prio.ifpriomap", value)
	}
	return ErrUnknownVersion
}