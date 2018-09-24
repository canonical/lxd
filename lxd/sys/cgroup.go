package sys

import (
	"fmt"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

// Detect CGroup support.
func (s *OS) initCGroup() {
	flags := []*bool{
		&s.CGroupBlkioController,
		&s.CGroupCPUController,
		&s.CGroupCPUacctController,
		&s.CGroupCPUsetController,
		&s.CGroupDevicesController,
		&s.CGroupFreezerController,
		&s.CGroupMemoryController,
		&s.CGroupNetPrioController,
		&s.CGroupPidsController,
		&s.CGroupSwapAccounting,
	}
	for i, flag := range flags {
		*flag = shared.PathExists("/sys/fs/cgroup/" + cGroups[i].path)
		if !*flag {
			logger.Warnf(cGroups[i].warn)
		}
	}
}

func cGroupMissing(name, message string) string {
	return fmt.Sprintf("Couldn't find the CGroup %s, %s.", name, message)
}

func cGroupDisabled(name, message string) string {
	return fmt.Sprintf("CGroup %s is disabled, %s.", name, message)
}

var cGroups = []struct {
	path string
	warn string
}{
	{"blkio", cGroupMissing("blkio", "I/O limits will be ignored")},
	{"cpu", cGroupMissing("CPU controller", "CPU time limits will be ignored")},
	{"cpuacct", cGroupMissing("CPUacct controller", "CPU accounting will not be available")},
	{"cpuset", cGroupMissing("CPUset controller", "CPU pinning will be ignored")},
	{"devices", cGroupMissing("devices controller", "device access control won't work")},
	{"freezer", cGroupMissing("freezer controller", "pausing/resuming containers won't work")},
	{"memory", cGroupMissing("memory controller", "memory limits will be ignored")},
	{"net_prio", cGroupMissing("network class controller", "network limits will be ignored")},
	{"pids", cGroupMissing("pids controller", "process limits will be ignored")},
	{"memory/memory.memsw.limit_in_bytes", cGroupDisabled("memory swap accounting", "swap limits will be ignored")},
}
