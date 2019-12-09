// +build linux,cgo,!agent

package sys

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

// Detect CGroup support.
func (s *OS) initCGroup() {
	flags := []*bool{
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

	flags_v2 := []*bool{
		//tell us if it's a v1 or v2 controller
		&s.CGroupMemoryControllerV2,
		&s.CGroupCPUControllerV2,
		&s.CGroupPidsControllerV2,
	}
	var j int = 0
	for i,flag := range flags  {
		if cGroups[i].path == "memory" || cGroups[i].path == "pids" || cGroups[i].path == "cpu" {
			//have to check v1 and v2
			//v1
			*flag = shared.PathExists("/sys/fs/cgroup/" + cGroups[i].path)
			*flags_v2[j] = false
			//then set flag for flags_v2 to be false
			//v2
			if !*flag  {
				//read this file to check which controllers are supported for v2
				//path := path.Join("/sys/fs/cgroup", "unified", "cgroup.controllers")
				//TODO-our: need to change this path for hybrid 
				path := path.Join("/sys/fs/cgroup",  "cgroup.controllers")
				file, err := os.Open(path)
				if err != nil {
					logger.Debugf("Can't open file")
				}
				defer file.Close()

				scanner := bufio.NewScanner(file)
				for scanner.Scan() {
					line_controllers:= scanner.Text()
					//if controller is within file, version will be 2
					if strings.Contains(line_controllers, cGroups[i].path) {
						*flag = true
						*flags_v2[j] = true
					}

				}
				j++
				if err := scanner.Err(); err != nil {
					logger.Debugf("Can't something")
				}

			}
		} else {

			*flag = shared.PathExists("/sys/fs/cgroup/" + cGroups[i].path)
		}

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
