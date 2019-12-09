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
)

type configItem struct {
	Key string
	Value string
}

// Get finds property values on a lxcContainer
func Get(c *lxc.Container, os *sys.OS, property Property) ([]string, error) {
	switch property {

	// Current Memory Usage
	case MemoryCurrent:
		if os.CGroupMemoryController == sys.CGroupV2 {
			return c.CgroupItem("memory.current"), nil
		} else {
			return c.CgroupItem("memory.usage_in_bytes"), nil
		}

	// Properties which have the same functionality for both v1 and v2
	case PidsCurrent:
		return c.CgroupItem("pids.current"), nil

	}



	return nil, fmt.Errorf("CGroup Property not supported for Get")
}

// Set sets a property on a lxcContainer
func Set(c *lxc.Container, property Property, value string) error {

	configs, e := SetConfigMap(property, value)
	if e != nil {
		return e
	}

	for _, rule :=  range configs {
		err := c.SetCgroupItem(rule.Key, rule.Value)

		if err != nil {
			return fmt.Errorf("Failure while trying to set property: %s", err)
		}
	}

	return nil
}

// SetConfigMap returns different cgroup configs to set a particular property
func SetConfigMap(property Property, value string) ([]configItem, error) {

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

	}

	return nil, fmt.Errorf("CGroup Property not supported for Set")
}
