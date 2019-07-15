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
