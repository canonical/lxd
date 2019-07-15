package device

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"github.com/lxc/lxd/shared"
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

// NetworkGetDevMTU retrieves the current MTU setting for a named network device.
func NetworkGetDevMTU(devName string) (uint64, error) {
	content, err := ioutil.ReadFile(fmt.Sprintf("/sys/class/net/%s/mtu", devName))
	if err != nil {
		return 0, err
	}

	// Parse value
	mtu, err := strconv.ParseUint(strings.TrimSpace(string(content)), 10, 32)
	if err != nil {
		return 0, err
	}

	return mtu, nil
}

// NetworkSetDevMTU sets the MTU setting for a named network device if different from current.
func NetworkSetDevMTU(devName string, mtu uint64) error {
	curMTU, err := NetworkGetDevMTU(devName)
	if err != nil {
		return err
	}

	// Only try and change the MTU if the requested mac is different to current one.
	if curMTU != mtu {
		_, err := shared.RunCommand("ip", "link", "set", "dev", devName, "mtu", fmt.Sprintf("%d", mtu))
		if err != nil {
			return err
		}
	}

	return nil
}

// NetworkGetDevMAC retrieves the current MAC setting for a named network device.
func NetworkGetDevMAC(devName string) (string, error) {
	content, err := ioutil.ReadFile(fmt.Sprintf("/sys/class/net/%s/address", devName))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(fmt.Sprintf("%s", content)), nil
}

// NetworkSetDevMAC sets the MAC setting for a named network device if different from current.
func NetworkSetDevMAC(devName string, mac string) error {
	curMac, err := NetworkGetDevMAC(devName)
	if err != nil {
		return err
	}

	// Only try and change the MAC if the requested mac is different to current one.
	if curMac != mac {
		_, err := shared.RunCommand("ip", "link", "set", "dev", devName, "address", mac)
		if err != nil {
			return err
		}
	}

	return nil
}

// NetworkGetHostDevice figures out whether there is an existing interface for the supplied
// parent device and VLAN ID and returns it. Otherwise just returns the parent device name.
func NetworkGetHostDevice(parent string, vlan string) string {
	// If no VLAN, just use the raw device
	if vlan == "" {
		return parent
	}

	// If no VLANs are configured, use the default pattern
	defaultVlan := fmt.Sprintf("%s.%s", parent, vlan)
	if !shared.PathExists("/proc/net/vlan/config") {
		return defaultVlan
	}

	// Look for an existing VLAN
	f, err := os.Open("/proc/net/vlan/config")
	if err != nil {
		return defaultVlan
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		// Only grab the lines we're interested in
		s := strings.Split(scanner.Text(), "|")
		if len(s) != 3 {
			continue
		}

		vlanIface := strings.TrimSpace(s[0])
		vlanID := strings.TrimSpace(s[1])
		vlanParent := strings.TrimSpace(s[2])

		if vlanParent == parent && vlanID == vlan {
			return vlanIface
		}
	}

	// Return the default pattern
	return defaultVlan
}

// NetworkRemoveInterface removes a network interface by name.
func NetworkRemoveInterface(nic string) error {
	_, err := shared.RunCommand("ip", "link", "del", "dev", nic)
	return err
}

// NetworkCreateVlanDeviceIfNeeded creates a VLAN device if doesn't already exist.
func NetworkCreateVlanDeviceIfNeeded(parent string, hostName string, vlan string) (bool, error) {
	if vlan != "" {
		if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", hostName)) {
			_, err := shared.RunCommand("ip", "link", "add", "link", parent, "name", hostName, "up", "type", "vlan", "id", vlan)
			if err != nil {
				return false, err
			}

			// Attempt to disable IPv6 router advertisement acceptance
			NetworkSysctlSet(fmt.Sprintf("ipv6/conf/%s/accept_ra", hostName), "0")

			// We created a new vlan interface, return true
			return true, nil
		}
	}

	return false, nil
}
