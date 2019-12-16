package cgroup

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	lxc "gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

var cgCgroup2SuperMagic int64 = 0x63677270

var cgControllers = map[string]Backend{}
var cgNamespace bool
var lxcCgroup2Support bool

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

// Supports indicates whether or not a given cgroup control knob is available.
// Note, we use "knob" instead of "controller" because this map holds
// controllers as well as new features for a given controller, i.e. you can
// have "blkio" which is a controller and "blkio.weight" which is a feature of
// the blkio controller.
func (info *Info) Supports(knob string) bool {
	_, ok := cgControllers[knob]
	return ok
}

// SupportsV1 indicated whether a given controller knob is available in the
// legacy hierarchy. Once we're fully ported this should be removed.
func (info *Info) SupportsV1(knob string) bool {
	val, ok := cgControllers[knob]
	if ok && val == V1 {
		return true
	}

	return false
}

// Log logs cgroup info
func (info *Info) Log() {
	logger.Infof(" - cgroup layout: %s", info.Mode())

	if !info.Supports("blkio") {
		logger.Warnf(" - Couldn't find the CGroup blkio, I/O limits will be ignored")
	}

	if !info.Supports("blkio.weight") {
		logger.Warnf(" - Couldn't find the CGroup blkio.weight, I/O weight limits will be ignored")
	}

	if !info.Supports("cpu") {
		logger.Warnf(" - Couldn't find the CGroup CPU controller, CPU time limits will be ignored")
	}

	if !info.Supports("cpuacct") {
		logger.Warnf(" - Couldn't find the CGroup CPUacct controller, CPU accounting will not be available")
	}

	if !info.Supports("cpuset") {
		logger.Warnf(" - Couldn't find the CGroup CPUset controller, CPU pinning will be ignored")
	}

	if !info.Supports("devices") {
		logger.Warnf(" - Couldn't find the CGroup devices controller, device access control won't work")
	}

	if !info.Supports("freezer") {
		logger.Warnf(" - Couldn't find the CGroup freezer controller, pausing/resuming containers won't work")
	}

	if !info.Supports("memory") {
		logger.Warnf(" - Couldn't find the CGroup memory controller, memory limits will be ignored")
	}

	if !info.Supports("net_prio") {
		logger.Warnf(" - Couldn't find the CGroup network class controller, network limits will be ignored")
	}

	if !info.Supports("pids") {
		logger.Warnf(" - Couldn't find the CGroup pids controller, process limits will be ignored")
	}

	if !info.Supports("memory.memsw.limit_in_bytes") {
		logger.Warnf(" - Couldn't find the CGroup memory swap accounting, swap limits will be ignored")
	}
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

	// Check for additional legacy cgroup features
	val, ok := cgControllers["blkio"]
	if ok && val == V1 && shared.PathExists("/sys/fs/cgroup/blkio/blkio.weight") {
		cgControllers["blkio.weight"] = V1
	}

	val, ok = cgControllers["memory"]
	if ok && val == V1 && shared.PathExists("/sys/fs/cgroup/memory/memory.memsw.limit_in_bytes") {
		cgControllers["memory.memsw.limit_in_bytes"] = V2
	}

	if hasV1 && hasV2 {
		cgLayout = CgroupsHybrid
		lxcCgroup2Support = lxc.HasApiExtension("cgroup2")
	} else if hasV1 {
		cgLayout = CgroupsLegacy
	} else if hasV2 {
		cgLayout = CgroupsUnified
		lxcCgroup2Support = lxc.HasApiExtension("cgroup2")
	}
}
