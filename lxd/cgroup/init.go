package cgroup

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/lxc/lxd/shared/logger"
)

var cgControllers = map[string]Backend{}

func init() {
	// Go through the list of resource controllers for LXD.
	selfCg, err := os.Open("/proc/self/cgroup")
	if err != nil {
		if os.IsNotExist(err) {
			logger.Warnf("System doesn't appear to support CGroups")
		} else {
			logger.Errorf("Unable to load list of cgroups: %v", err)
		}

		return
	}
	defer selfCg.Close()

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
			if !os.IsNotExist(err) {
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
		}
	}
}
