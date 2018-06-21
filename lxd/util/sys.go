package util

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	log "github.com/lxc/lxd/shared/log15"
	golxc "gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/osarch"
)

// GetArchitectures returns the list of supported architectures.
func GetArchitectures() ([]int, error) {
	architectures := []int{}

	architectureName, err := osarch.ArchitectureGetLocal()
	if err != nil {
		return nil, err
	}

	architecture, err := osarch.ArchitectureId(architectureName)
	if err != nil {
		return nil, err
	}
	architectures = append(architectures, architecture)

	personalities, err := osarch.ArchitecturePersonalities(architecture)
	if err != nil {
		return nil, err
	}
	for _, personality := range personalities {
		architectures = append(architectures, personality)
	}
	return architectures, nil
}

// GetIdmapSet reads the uid/gid allocation.
func GetIdmapSet() *idmap.IdmapSet {
	idmapSet, err := idmap.DefaultIdmapSet("", "")
	if err != nil {
		logger.Warn("Error reading default uid/gid map", log.Ctx{"err": err.Error()})
		logger.Warnf("Only privileged containers will be able to run")
		idmapSet = nil
	} else {
		kernelIdmapSet, err := idmap.CurrentIdmapSet()
		if err == nil {
			logger.Infof("Kernel uid/gid map:")
			for _, lxcmap := range kernelIdmapSet.ToLxcString() {
				logger.Infof(fmt.Sprintf(" - %s", lxcmap))
			}
		}

		if len(idmapSet.Idmap) == 0 {
			logger.Warnf("No available uid/gid map could be found")
			logger.Warnf("Only privileged containers will be able to run")
			idmapSet = nil
		} else {
			logger.Infof("Configured LXD uid/gid map:")
			for _, lxcmap := range idmapSet.Idmap {
				suffix := ""

				if lxcmap.Usable() != nil {
					suffix = " (unusable)"
				}

				for _, lxcEntry := range lxcmap.ToLxcString() {
					logger.Infof(" - %s%s", lxcEntry, suffix)
				}
			}

			err = idmapSet.Usable()
			if err != nil {
				logger.Warnf("One or more uid/gid map entry isn't usable (typically due to nesting)")
				logger.Warnf("Only privileged containers will be able to run")
				idmapSet = nil
			}
		}
	}
	return idmapSet
}

// RuntimeLiblxcVersionAtLeast checks if the system's liblxc matches the
// provided version requirement
func RuntimeLiblxcVersionAtLeast(major int, minor int, micro int) bool {
	version := golxc.Version()
	version = strings.Replace(version, " (devel)", "-devel", 1)
	parts := strings.Split(version, ".")
	partsLen := len(parts)
	if partsLen == 0 {
		return false
	}

	develParts := strings.Split(parts[partsLen-1], "-")
	if len(develParts) == 2 && develParts[1] == "devel" {
		return true
	}

	maj := -1
	min := -1
	mic := -1

	for i, v := range parts {
		if i > 2 {
			break
		}

		num, err := strconv.Atoi(v)
		if err != nil {
			return false
		}

		switch i {
		case 0:
			maj = num
		case 1:
			min = num
		case 2:
			mic = num
		}
	}

	/* Major version is greater. */
	if maj > major {
		return true
	}

	if maj < major {
		return false
	}

	/* Minor number is greater.*/
	if min > minor {
		return true
	}

	if min < minor {
		return false
	}

	/* Patch number is greater. */
	if mic > micro {
		return true
	}

	if mic < micro {
		return false
	}

	return true
}

// GetExecPath returns the path to the current binary
func GetExecPath() string {
	execPath := os.Getenv("LXD_EXEC_PATH")
	if execPath != "" {
		return execPath
	}

	execPath, err := os.Readlink("/proc/self/exe")
	if err != nil {
		execPath = "bad-exec-path"
	}
	return execPath
}
