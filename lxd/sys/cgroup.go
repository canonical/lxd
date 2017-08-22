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

var cGroups = []struct {
	path string
	warn string
}{
	{"blkio/", cGroupMissing("blkio", "I/O limits will be ignored")},
	{"cpu/", cGroupMissing("CPU controller", "CPU time limits will be ignored")},
}
