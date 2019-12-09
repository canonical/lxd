package cgroup

import (
	"fmt"

	"github.com/lxc/lxd/lxd/sys"
	"gopkg.in/lxc/go-lxc.v2"
)

type Property int

const (
	PidsCurrent Property = iota
	PidsMax
	MemoryCurrent
	CpuacctUsage
	MemoryLimitInBytes
	MemorySoftLimitInBytes
	BlkioWeight
	MemorySwappiness
	CpuShares
	CpuCfsPeriodUs
	CpuCfsQuotaUs
	NetPrioIfPrioMap
	MemoryMemswLimitInBytes
	MemoryMemswUsageInBytes
	MemoryMemswMaxUsageInBytes
)

type configItem struct {
	Key   string
	Value string
}

// Get finds property values on a lxcContainer
func Get(c *lxc.Container, os *sys.OS, property Property) ([]string, error) {
	switch property {

	// Current Memory Usage
	case MemoryCurrent:
		if os.CGroupMemoryController == sys.CGroupV2 {
			return c.CgroupItem("memory.current"), nil
		}
		return c.CgroupItem("memory.usage_in_bytes"), nil

	// Properties which have the same functionality for both v1 and v2
	case PidsCurrent:
		return c.CgroupItem("pids.current"), nil
	case MemoryLimitInBytes:
		if os.CGroupMemoryController == sys.CGroupV2 {
			return c.CgroupItem("memory.max"), nil
		}
		return c.CgroupItem("memory.max_limit_in_bytes"), nil

	case MemorySoftLimitInBytes:
		if os.CGroupMemoryController == sys.CGroupV2 {
			return c.CgroupItem("memory.low"), nil
		}
		return c.CgroupItem("memory.soft_limit_in_bytes"), nil
	case MemoryMemswLimitInBytes:
		if os.CGroupMemoryController == sys.CGroupV2 {

		}
	}

	return nil, fmt.Errorf("CGroup Property not supported for Get")
}

// Set sets a property on a lxcContainer
func Set(c *lxc.Container, property Property, value string, os *sys.OS) error {

	configs, e := SetConfigMap(property, value, os)
	if e != nil {
		return e
	}

	for _, rule := range configs {
		err := c.SetCgroupItem(rule.Key, rule.Value)

		if err != nil {
			return fmt.Errorf("Failure while trying to set property: %s", err)
		}
	}

	return nil
}

// SetConfigMap returns different cgroup configs to set a particular property
func SetConfigMap(property Property, value string, os *sys.OS) ([]configItem, error) {

	switch property {

	// Properties which have the same functionality for both v1 and v2
	case PidsCurrent:
		return []configItem{
			{Key: "pids.current", Value: value},
		}, nil

	case PidsMax:
		return []configItem{
			{Key: "pids.max", Value: value},
		}, nil

	case BlkioWeight:
		return []configItem{
			{Key: "blkio.weight", Value: value},
		}, nil

	case NetPrioIfPrioMap:
		return []configItem{
			{Key: "net_prio.ifpriomap", Value: value},
		}, nil

	case CpuShares:
		//need to check os because cpu
		if os.CGroupMemoryController == sys.CGroupV2 {
			return []configItem{
				{Key: "cpu.weight", Value: value},
			}, nil
		}
		return []configItem{
			{Key: "cpu.shares", Value: value},
		}, nil

	}

	return nil, fmt.Errorf("CGroup Property not supported for Set")
}