// +build linux,cgo,!agent

package sys

import (
	"fmt"
	"io/ioutil"
	"strings"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)


// Detect CGroup support.
func (s *OS) initCGroup() {
	cgroupsinfo := []*CGroupInfo{
		&s.CGroupBlkioController,
		&s.CGroupBlkioWeightController,
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

	// Read all v2 controllers for later parsing
	v2controllers := ""
	contents, err := ioutil.ReadFile("/sys/fs/cgroup/cgroup.controllers")
	if err != nil {
		v2controllers = string(contents)
	}

	for i, info := range cgroupsinfo  {
		if shared.PathExists("/sys/fs/cgroup/" + cGroups[i].path) {
			// Check v1 support
			*info = CGroupV1
		} else if strings.Contains(v2controllers, cGroups[i].path) {
			// Check v2 support
			*info = CGroupV2
		} else {
			*info = CGroupDisabled
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
	{"blkio/blkio.weight", cGroupMissing("blkio.weight", "I/O weight limits will be ignored")},
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
