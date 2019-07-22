package device

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
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
func NetworkCreateVlanDeviceIfNeeded(parent string, vlanDevice string, vlanID string) (bool, error) {
	if vlanID != "" {
		if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", vlanDevice)) {
			// Bring the parent interface up so we can add a vlan to it.
			_, err := shared.RunCommand("ip", "link", "set", "dev", parent, "up")
			if err != nil {
				return false, fmt.Errorf("Failed to bring up parent %s: %v", parent, err)
			}

			// Add VLAN interface on top of parent.
			_, err = shared.RunCommand("ip", "link", "add", "link", parent, "name", vlanDevice, "up", "type", "vlan", "id", vlanID)
			if err != nil {
				return false, err
			}

			// Attempt to disable IPv6 router advertisement acceptance
			NetworkSysctlSet(fmt.Sprintf("ipv6/conf/%s/accept_ra", vlanDevice), "0")

			// We created a new vlan interface, return true
			return true, nil
		}
	}

	return false, nil
}

// networkSnapshotPhysicalNic records properties of the NIC to volatile so they can be restored later.
func networkSnapshotPhysicalNic(hostName string, volatile map[string]string) error {
	// Store current MTU for restoration on detach.
	mtu, err := NetworkGetDevMTU(hostName)
	if err != nil {
		return err
	}
	volatile["last_state.mtu"] = fmt.Sprintf("%d", mtu)

	// Store current MAC for restoration on detach
	mac, err := NetworkGetDevMAC(hostName)
	if err != nil {
		return err
	}
	volatile["last_state.hwaddr"] = mac
	return nil
}

// networkRestorePhysicalNic restores NIC properties from volatile to what they were before it was attached.
func networkRestorePhysicalNic(hostName string, volatile map[string]string) error {
	// If we created the "physical" device and then it should be removed.
	if shared.IsTrue(volatile["last_state.created"]) {
		return NetworkRemoveInterface(hostName)
	}

	// Bring the interface down, as this is sometimes needed to change settings on the nic.
	_, err := shared.RunCommand("ip", "link", "set", "dev", hostName, "down")
	if err != nil {
		return fmt.Errorf("Failed to bring down \"%s\": %v", hostName, err)
	}

	// If MTU value is specified then there is an original MTU that needs restoring.
	if volatile["last_state.mtu"] != "" {
		mtuInt, err := strconv.ParseUint(volatile["last_state.mtu"], 10, 32)
		if err != nil {
			return fmt.Errorf("Failed to convert mtu for \"%s\" mtu \"%s\": %v", hostName, volatile["last_state.mtu"], err)
		}

		err = NetworkSetDevMTU(hostName, mtuInt)
		if err != nil {
			return fmt.Errorf("Failed to restore physical dev \"%s\" mtu to \"%d\": %v", hostName, mtuInt, err)
		}
	}

	// If MAC value is specified then there is an original MAC that needs restoring.
	if volatile["last_state.hwaddr"] != "" {
		err := NetworkSetDevMAC(hostName, volatile["last_state.hwaddr"])
		if err != nil {
			return fmt.Errorf("Failed to restore physical dev \"%s\" mac to \"%s\": %v", hostName, volatile["last_state.hwaddr"], err)
		}
	}

	return nil
}

// NetworkRandomDevName returns a random device name with prefix.
// If the random string combined with the prefix exceeds 13 characters then empty string is returned.
// This is to ensure we support buggy dhclient applications: https://bugs.debian.org/cgi-bin/bugreport.cgi?bug=858580
func NetworkRandomDevName(prefix string) string {
	// Return a new random veth device name
	randBytes := make([]byte, 4)
	rand.Read(randBytes)
	iface := prefix + hex.EncodeToString(randBytes)
	if len(iface) > 13 {
		return ""
	}

	return iface
}

// NetworkAttachInterface attaches an interface to a bridge.
func NetworkAttachInterface(netName string, devName string) error {
	if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", netName)) {
		_, err := shared.RunCommand("ip", "link", "set", "dev", devName, "master", netName)
		if err != nil {
			return err
		}
	} else {
		_, err := shared.RunCommand("ovs-vsctl", "port-to-br", devName)
		if err != nil {
			_, err := shared.RunCommand("ovs-vsctl", "add-port", netName, devName)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
