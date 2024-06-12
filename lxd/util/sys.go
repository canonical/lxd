//go:build linux && cgo && !agent

package util

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared/osarch"
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

	architectures = append(architectures, personalities...)

	return architectures, nil
}

// GetExecPath returns the path to the current binary.
func GetExecPath() string {
	execPath := os.Getenv("LXD_EXEC_PATH")
	if execPath != "" {
		return execPath
	}

	execPath, err := os.Readlink("/proc/self/exe")
	if err != nil {
		execPath = "bad-exec-path"
	}

	// The execPath from /proc/self/exe can end with " (deleted)" if the lxd binary has been removed/changed
	// since the lxd process was started, strip this so that we only return a valid path.
	return strings.TrimSuffix(execPath, " (deleted)")
}

// ReplaceDaemon replaces the LXD process.
func ReplaceDaemon() error {
	err := unix.Exec(GetExecPath(), os.Args, os.Environ())
	if err != nil {
		return err
	}

	return nil
}

// GetQemuFwPaths returns a list of directory paths to search for QEMU firmware files.
func GetQemuFwPaths() ([]string, error) {
	var qemuFwPaths []string

	for _, v := range []string{"LXD_QEMU_FW_PATH", "LXD_OVMF_PATH"} {
		searchPaths := os.Getenv(v)
		if searchPaths == "" {
			continue
		}

		qemuFwPaths = append(qemuFwPaths, strings.Split(searchPaths, ":")...)
	}

	// Append default paths after ones extracted from env vars so they take precedence.
	qemuFwPaths = append(qemuFwPaths, "/usr/share/OVMF", "/usr/share/seabios")

	count := 0
	for i, path := range qemuFwPaths {
		var err error
		resolvedPath, err := filepath.EvalSymlinks(path)
		if err != nil {
			// don't fail, just skip as some search paths can be optional
			continue
		}

		count++
		qemuFwPaths[i] = resolvedPath
	}

	// We want to have at least one valid path to search for firmware.
	if count == 0 {
		return nil, fmt.Errorf("Failed to find a valid search path for firmware")
	}

	return qemuFwPaths, nil
}
