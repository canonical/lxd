package util

import (
	"bufio"
	"fmt"
	"os"
	"strings"

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

// HasFilesystem checks whether a given filesystem is already supported
// by the kernel. Note that if the filesystem is a module, you may need to
// load it first.
func HasFilesystem(filesystem string) bool {
	file, err := os.Open("/proc/filesystems")
	if err != nil {
		return false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(strings.TrimSpace(scanner.Text()))
		entry := fields[len(fields)-1]

		if entry == filesystem {
			return true
		}
	}

	return false
}
