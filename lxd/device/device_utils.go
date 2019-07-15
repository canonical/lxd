package device

import (
	"fmt"
	"io/ioutil"
)

// NetworkSysctlGet retrieves the value of a sysctl file in /proc/sys/net.
func NetworkSysctlGet(path string) (string, error) {
	// Read the current content
	content, err := ioutil.ReadFile(fmt.Sprintf("/proc/sys/net/%s", path))
	if err != nil {
		return "", err
	}

	return string(content), nil
}

// NetworkSysctlSet writes a value to a sysctl file in /proc/sys/net.
func NetworkSysctlSet(path string, value string) error {
	// Get current value
	current, err := NetworkSysctlGet(path)
	if err == nil && current == value {
		// Nothing to update
		return nil
	}

	return ioutil.WriteFile(fmt.Sprintf("/proc/sys/net/%s", path), []byte(value), 0)
}
