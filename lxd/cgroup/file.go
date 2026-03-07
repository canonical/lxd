package cgroup

import (
	"fmt"
	"os"
	"strings"

	"github.com/canonical/lxd/shared"
)

// NewFileReadWriter returns a CGroup instance using the filesystem as its backend.
func NewFileReadWriter(pid int) (*CGroup, error) {
	// Setup the read/writer struct.
	rw := fileReadWriter{}

	// Locate the base path for each controller.
	rw.paths = map[string]string{}

	controllers, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return nil, err
	}

	hasUnifiedMount := shared.PathExists("/sys/fs/cgroup/unified")
	cgroupBasePath := "/sys/fs/cgroup"
	if hasUnifiedMount {
		cgroupBasePath = "/sys/fs/cgroup/unified"
	}

	for line := range strings.SplitSeq(string(controllers), "\n") {
		// Skip empty lines.
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Extract the fields.
		hierarchyID, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}

		controllerList, cgroupPath, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}

		// Determine the mount path.
		if hierarchyID == "0" {
			cgroupPath, _ = strings.CutSuffix(cgroupPath, "/init.scope")
			rw.paths["unified"] = cgroupBasePath + "/" + cgroupPath
		} else {
			path := "/sys/fs/cgroup/" + controllerList + "/" + cgroupPath

			// Add the controllers individually.
			for ctrl := range strings.SplitSeq(controllerList, ",") {
				rw.paths[ctrl] = path
			}
		}
	}

	cg, err := New(&rw)
	if err != nil {
		return nil, err
	}

	cg.UnifiedCapable = true // cgroup2: introduced in lxc 4.0.0
	return cg, nil
}

type fileReadWriter struct {
	paths map[string]string
}

// path returns the full path for a cgroup key under the given controller.
func (rw *fileReadWriter) path(controller string, key string) (string, error) {
	base := rw.paths[controller]
	if cgLayout == CgroupsUnified {
		base = rw.paths["unified"]
	}

	if base == "" {
		return "", ErrControllerMissing
	}

	return base + "/" + key, nil
}

// Get returns the value of a cgroup key for a specific controller.
func (rw *fileReadWriter) Get(version Backend, controller string, key string) (string, error) {
	path, err := rw.path(controller, key)
	if err != nil {
		return "", err
	}

	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(value)), nil
}

// Set applies the given value to a cgroup key for a specific controller.
func (rw *fileReadWriter) Set(version Backend, controller string, key string, value string) error {
	path, err := rw.path(controller, key)
	if err != nil {
		return err
	}

	return os.WriteFile(path, []byte(value), 0600)
}
