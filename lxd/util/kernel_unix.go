//go:build !windows && !linux
package util

import (
	"fmt"

	"github.com/lxc/lxd/shared"
)

// LoadModule loads the kernel module with the given name, by invoking
// kldload.
func LoadModule(module string) error {
	_, err := shared.RunCommand("kldstat", "-n", module)
	if err != nil {
		_, err := shared.RunCommand("kldload", module)
		return err
	}
	return nil
}

// SupportsFilesystem checks whether a given filesystem is already supported
// by the kernel. This is unimplemented for other Unices, since it's only used
// by the daemon, which only runs on Linux
func SupportsFilesystem(filesystem string) bool {
	return false
}

// HugepagesPath attempts to locate the mount point of the hugepages filesystem.
// this is unimplemented on other Unices, since it's only used in the daemon,
// which only runs on Linux
func HugepagesPath() (string, error) {
	return "", fmt.Errorf("Not Implemented")
}
