package util

import (
	"fmt"

	"github.com/lxc/lxd/shared"
)

// LoadModule loads the kernel module with the given name, by invoking
// modprobe.
func LoadModule(module string) error {
	if shared.PathExists(fmt.Sprintf("/sys/module/%s", module)) {
		return nil
	}

	_, err := shared.RunCommand("modprobe", module)
	return err
}
