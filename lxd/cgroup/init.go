package cgroup

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/lxd/shared/logger"
)

var cgCgroup2SuperMagic int64 = 0x63677270

var cgControllers = map[string]Backend{}
var cgNamespace bool

// Layout determines the cgroup layout on this system
type Layout int

const (
	// CgroupsDisabled indicates that cgroups are not supported
	CgroupsDisabled = iota
	// CgroupsUnified indicates that this is a pure cgroup2 layout
	CgroupsUnified
	// CgroupsHybrid indicates that this is a mixed cgroup1 and cgroup2 layout
	CgroupsHybrid
	// CgroupsLegacy indicates that this is a pure cgroup1 layout
	CgroupsLegacy
)

var cgLayout Layout

// Info contains system cgroup information
type Info struct {
	// Layout is one of CgroupsDisabled, CgroupsUnified, CgroupsHybrid, CgroupsLegacy
	Layout Layout

	// Namespacing indicates support for the cgroup namespace
	Namespacing bool
}

// GetInfo returns basic system cgroup information
func GetInfo() Info {
	info := Info{}
	info.Namespacing = cgNamespace
	info.Layout = cgLayout

	return info
}

// Mode returns the cgroup layout name
func (info *Info) Mode() string {
	switch info.Layout {
	case CgroupsDisabled:
		return "disabled"
	case CgroupsUnified:
		return "cgroup2"
	case CgroupsHybrid:
		return "hybrid"
	case CgroupsLegacy:
		return "legacy"
	}

	return "unknown"
}

func init() {
	_, err := os.Stat("/proc/self/ns/cgroup")
	if err == nil {
		cgNamespace = true
	}

	// Go through the list of resource controllers for LXD.
	selfCg, err := os.Open("/proc/self/cgroup")
	if err != nil {
		if os.IsNotExist(err) {
			logger.Warnf("System doesn't appear to support CGroups")
		} else {
			logger.Errorf("Unable to load list of cgroups: %v", err)
		}

		cgLayout = CgroupsDisabled
		return
	}
	defer selfCg.Close()

	hasV1 := false
	hasV2 := false
	// Go through the file line by line.
	scanSelfCg := bufio.NewScanner(selfCg)
	for scanSelfCg.Scan() {
		line := strings.TrimSpace(scanSelfCg.Text())
		fields := strings.SplitN(line, ":", 3)

		// Deal with the V1 controllers.
		if fields[1] != "" {
			controllers := strings.Split(fields[1], ",")
			for _, controller := range controllers {
				cgControllers[controller] = V1
			}

			hasV1 = true
			continue
		}

		// Parse V2 controllers.
		path := fields[2]
		hybridPath := filepath.Join(cgPath, "unified", path, "cgroup.controllers")
		dedicatedPath := filepath.Join(cgPath, path, "cgroup.controllers")

		controllers, err := os.Open(hybridPath)
		if err != nil {
			if !os.IsNotExist(err) {
				logger.Errorf("Unable to load cgroup.controllers")
				return
			}

			controllers, err = os.Open(dedicatedPath)
			if err != nil && !os.IsNotExist(err) {
				logger.Errorf("Unable to load cgroup.controllers")
				return
			}
		}

		if err == nil {
			// Record the fact that V2 is present at all.
			cgControllers["unified"] = V2

			scanControllers := bufio.NewScanner(controllers)
			for scanControllers.Scan() {
				line := strings.TrimSpace(scanSelfCg.Text())
				cgControllers[line] = V2
			}
			hasV2 = true
		}
	}

	if hasV1 && hasV2 {
		cgLayout = CgroupsHybrid
	} else if hasV1 {
		cgLayout = CgroupsLegacy
	} else if hasV2 {
		cgLayout = CgroupsUnified
	}
}
