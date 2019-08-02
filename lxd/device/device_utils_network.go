package device

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/units"
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

// networkCreateVethPair creates and configures a veth pair. It accepts the name of the host side
// interface as a parameter and returns the peer interface name.
func networkCreateVethPair(hostName string, m config.Device) (string, error) {
	peerName := NetworkRandomDevName("veth")

	_, err := shared.RunCommand("ip", "link", "add", "dev", hostName, "type", "veth", "peer", "name", peerName)
	if err != nil {
		return "", fmt.Errorf("Failed to create the veth interfaces %s and %s: %s", hostName, peerName, err)
	}

	_, err = shared.RunCommand("ip", "link", "set", "dev", hostName, "up")
	if err != nil {
		NetworkRemoveInterface(hostName)
		return "", fmt.Errorf("Failed to bring up the veth interface %s: %s", hostName, err)
	}

	// Set the MAC address on peer.
	if m["hwaddr"] != "" {
		_, err := shared.RunCommand("ip", "link", "set", "dev", peerName, "address", m["hwaddr"])
		if err != nil {
			NetworkRemoveInterface(peerName)
			return "", fmt.Errorf("Failed to set the MAC address: %s", err)
		}
	}

	// Set the MTU on peer.
	if m["mtu"] != "" {
		_, err := shared.RunCommand("ip", "link", "set", "dev", peerName, "mtu", m["mtu"])
		if err != nil {
			NetworkRemoveInterface(peerName)
			return "", fmt.Errorf("Failed to set the MTU: %s", err)
		}
	}

	return peerName, nil
}

// networkSetupHostVethDevice configures a nic device's host side veth settings.
func networkSetupHostVethDevice(device config.Device, oldDevice config.Device, v map[string]string) error {
	// If not configured, check if volatile data contains the most recently added host_name.
	if device["host_name"] == "" {
		device["host_name"] = v["host_name"]
	}

	// If not configured, check if volatile data contains the most recently added hwaddr.
	if device["hwaddr"] == "" {
		device["hwaddr"] = v["hwaddr"]
	}

	// Check whether host device resolution succeeded.
	if device["host_name"] == "" {
		return fmt.Errorf("Failed to find host side veth name for device \"%s\"", device["name"])
	}

	// Refresh tc limits.
	err := networkSetVethLimits(device)
	if err != nil {
		return err
	}

	// If oldDevice provided, remove old routes if any remain.
	if oldDevice != nil {
		// If not configured, copy the volatile host_name into old device to support live updates.
		if oldDevice["host_name"] == "" {
			oldDevice["host_name"] = v["host_name"]
		}

		// If not configured, copy the volatile host_name into old device to support live updates.
		if oldDevice["hwaddr"] == "" {
			oldDevice["hwaddr"] = v["hwaddr"]
		}

		networkRemoveVethRoutes(oldDevice)
	}

	// Setup static routes to container.
	err = networkSetVethRoutes(device)
	if err != nil {
		return err
	}

	return nil
}

// networkSetVethRoutes applies any static routes configured from the host to the container nic.
func networkSetVethRoutes(m config.Device) error {
	// Decide whether the route should point to the veth parent or the bridge parent.
	routeDev := m["host_name"]
	if m["nictype"] == "bridged" {
		routeDev = m["parent"]
	}

	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", routeDev)) {
		return fmt.Errorf("Unknown or missing host side route interface: %s", routeDev)
	}

	// Add additional IPv4 routes (using boot proto to avoid conflicts with network static routes)
	if m["ipv4.routes"] != "" {
		for _, route := range strings.Split(m["ipv4.routes"], ",") {
			route = strings.TrimSpace(route)
			_, err := shared.RunCommand("ip", "-4", "route", "add", route, "dev", routeDev, "proto", "boot")
			if err != nil {
				return err
			}
		}
	}

	// Add additional IPv6 routes (using boot proto to avoid conflicts with network static routes)
	if m["ipv6.routes"] != "" {
		for _, route := range strings.Split(m["ipv6.routes"], ",") {
			route = strings.TrimSpace(route)
			_, err := shared.RunCommand("ip", "-6", "route", "add", route, "dev", routeDev, "proto", "boot")
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// networkRemoveVethRoutes removes any routes created for this device on the host that were first added
// with networkSetVethRoutes(). Expects to be passed the device config from the oldExpandedDevices.
func networkRemoveVethRoutes(m config.Device) {
	// Decide whether the route should point to the veth parent or the bridge parent
	routeDev := m["host_name"]
	if m["nictype"] == "bridged" {
		routeDev = m["parent"]
	}

	if m["ipv4.routes"] != "" || m["ipv6.routes"] != "" {
		if routeDev == "" {
			logger.Errorf("Failed to remove static routes as route dev isn't set")
			return
		}

		if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", routeDev)) {
			return //Routes will already be gone if device doesn't exist.
		}
	}

	// Remove IPv4 routes
	if m["ipv4.routes"] != "" {
		for _, route := range strings.Split(m["ipv4.routes"], ",") {
			route = strings.TrimSpace(route)
			_, err := shared.RunCommand("ip", "-4", "route", "flush", route, "dev", routeDev, "proto", "boot")
			if err != nil {
				logger.Errorf("Failed to remove static route: %s to %s: %s", route, routeDev, err)
			}
		}
	}

	// Remove IPv6 routes
	if m["ipv6.routes"] != "" {
		for _, route := range strings.Split(m["ipv6.routes"], ",") {
			route = strings.TrimSpace(route)
			_, err := shared.RunCommand("ip", "-6", "route", "flush", route, "dev", routeDev, "proto", "boot")
			if err != nil {
				logger.Errorf("Failed to remove static route: %s to %s: %s", route, routeDev, err)
			}
		}
	}
}

// networkSetVethLimits applies any network rate limits to the veth device specified in the config.
func networkSetVethLimits(m config.Device) error {
	var err error

	veth := m["host_name"]
	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", veth)) {
		return fmt.Errorf("Unknown or missing host side veth: %s", veth)
	}

	// Apply max limit
	if m["limits.max"] != "" {
		m["limits.ingress"] = m["limits.max"]
		m["limits.egress"] = m["limits.max"]
	}

	// Parse the values
	var ingressInt int64
	if m["limits.ingress"] != "" {
		ingressInt, err = units.ParseBitSizeString(m["limits.ingress"])
		if err != nil {
			return err
		}
	}

	var egressInt int64
	if m["limits.egress"] != "" {
		egressInt, err = units.ParseBitSizeString(m["limits.egress"])
		if err != nil {
			return err
		}
	}

	// Clean any existing entry
	shared.RunCommand("tc", "qdisc", "del", "dev", veth, "root")
	shared.RunCommand("tc", "qdisc", "del", "dev", veth, "ingress")

	// Apply new limits
	if m["limits.ingress"] != "" {
		out, err := shared.RunCommand("tc", "qdisc", "add", "dev", veth, "root", "handle", "1:0", "htb", "default", "10")
		if err != nil {
			return fmt.Errorf("Failed to create root tc qdisc: %s", out)
		}

		out, err = shared.RunCommand("tc", "class", "add", "dev", veth, "parent", "1:0", "classid", "1:10", "htb", "rate", fmt.Sprintf("%dbit", ingressInt))
		if err != nil {
			return fmt.Errorf("Failed to create limit tc class: %s", out)
		}

		out, err = shared.RunCommand("tc", "filter", "add", "dev", veth, "parent", "1:0", "protocol", "all", "u32", "match", "u32", "0", "0", "flowid", "1:1")
		if err != nil {
			return fmt.Errorf("Failed to create tc filter: %s", out)
		}
	}

	if m["limits.egress"] != "" {
		out, err := shared.RunCommand("tc", "qdisc", "add", "dev", veth, "handle", "ffff:0", "ingress")
		if err != nil {
			return fmt.Errorf("Failed to create ingress tc qdisc: %s", out)
		}

		out, err = shared.RunCommand("tc", "filter", "add", "dev", veth, "parent", "ffff:0", "protocol", "all", "u32", "match", "u32", "0", "0", "police", "rate", fmt.Sprintf("%dbit", egressInt), "burst", "1024k", "mtu", "64kb", "drop")
		if err != nil {
			return fmt.Errorf("Failed to create ingress tc qdisc: %s", out)
		}
	}

	return nil
}

// NetworkValidAddress validates an IP address string. If string is empty, returns valid.
func NetworkValidAddress(value string) error {
	if value == "" {
		return nil
	}

	ip := net.ParseIP(value)
	if ip == nil {
		return fmt.Errorf("Not an IP address: %s", value)
	}

	return nil
}

// NetworkValidAddressV4 validates an IPv4 addresss string. If string is empty, returns valid.
func NetworkValidAddressV4(value string) error {
	if value == "" {
		return nil
	}

	ip := net.ParseIP(value)
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("Not an IPv4 address: %s", value)
	}

	return nil
}

// NetworkValidAddressV6 validates an IPv6 addresss string. If string is empty, returns valid.
func NetworkValidAddressV6(value string) error {
	if value == "" {
		return nil
	}

	ip := net.ParseIP(value)
	if ip == nil || ip.To4() != nil {
		return fmt.Errorf("Not an IPv6 address: %s", value)
	}

	return nil
}

// NetworkValidAddressV4List validates a comma delimited list of IPv4 addresses.
func NetworkValidAddressV4List(value string) error {
	for _, v := range strings.Split(value, ",") {
		v = strings.TrimSpace(v)
		err := NetworkValidAddressV4(v)
		if err != nil {
			return err
		}
	}
	return nil
}

//NetworkValidAddressV6List validates a comma delimited list of IPv6 addresses.
func NetworkValidAddressV6List(value string) error {
	for _, v := range strings.Split(value, ",") {
		v = strings.TrimSpace(v)
		err := NetworkValidAddressV6(v)
		if err != nil {
			return err
		}
	}
	return nil
}

// NetworkValidNetworkV4 validates an IPv4 CIDR string. If string is empty, returns valid.
func NetworkValidNetworkV4(value string) error {
	if value == "" {
		return nil
	}

	ip, subnet, err := net.ParseCIDR(value)
	if err != nil {
		return err
	}

	if ip.To4() == nil {
		return fmt.Errorf("Not an IPv4 network: %s", value)
	}

	if ip.String() != subnet.IP.String() {
		return fmt.Errorf("Not an IPv4 network address: %s", value)
	}

	return nil
}

// NetworkValidNetworkV6 validates an IPv6 CIDR string. If string is empty, returns valid.
func NetworkValidNetworkV6(value string) error {
	if value == "" {
		return nil
	}

	ip, subnet, err := net.ParseCIDR(value)
	if err != nil {
		return err
	}

	if ip == nil || ip.To4() != nil {
		return fmt.Errorf("Not an IPv6 network: %s", value)
	}

	if ip.String() != subnet.IP.String() {
		return fmt.Errorf("Not an IPv6 network address: %s", value)
	}

	return nil
}

// NetworkValidNetworkV4List validates a comma delimited list of IPv4 CIDR strings.
func NetworkValidNetworkV4List(value string) error {
	for _, network := range strings.Split(value, ",") {
		network = strings.TrimSpace(network)
		err := NetworkValidNetworkV4(network)
		if err != nil {
			return err
		}
	}

	return nil
}

// NetworkValidNetworkV6List validates a comma delimited list of IPv6 CIDR strings.
func NetworkValidNetworkV6List(value string) error {
	for _, network := range strings.Split(value, ",") {
		network = strings.TrimSpace(network)
		err := NetworkValidNetworkV6(network)
		if err != nil {
			return err
		}
	}

	return nil
}

// NetworkSRIOVGetFreeVFInterface checks the contents of the VF directory to find a free VF
// interface name that belongs to the same device and port as the parent.
// Returns VF interface name or empty string if no free interface found.
func NetworkSRIOVGetFreeVFInterface(reservedDevices map[string]struct{}, vfListPath string, pfDevID []byte, pfDevPort []byte) (string, error) {
	ents, err := ioutil.ReadDir(vfListPath)
	if err != nil {
		return "", err
	}

	for _, ent := range ents {
		// We can't use this VF interface as it is reserved by another device.
		if _, exists := reservedDevices[ent.Name()]; exists {
			continue
		}

		// Get VF dev_port and dev_id values.
		vfDevPort, err := ioutil.ReadFile(fmt.Sprintf("%s/%s/dev_port", vfListPath, ent.Name()))
		if err != nil {
			return "", err
		}

		vfDevID, err := ioutil.ReadFile(fmt.Sprintf("%s/%s/dev_id", vfListPath, ent.Name()))
		if err != nil {
			return "", err
		}

		// Skip VFs if they do not relate to the same device and port as the parent PF.
		// Some card vendors change the device ID for each port.
		if bytes.Compare(pfDevPort, vfDevPort) != 0 || bytes.Compare(pfDevID, vfDevID) != 0 {
			continue
		}

		return ent.Name(), nil
	}

	return "", nil
}

// networkParsePortRange validates a port range in the form n-n.
func networkParsePortRange(r string) (int64, int64, error) {
	entries := strings.Split(r, "-")
	if len(entries) > 2 {
		return -1, -1, fmt.Errorf("Invalid port range %s", r)
	}

	base, err := strconv.ParseInt(entries[0], 10, 64)
	if err != nil {
		return -1, -1, err
	}

	size := int64(1)
	if len(entries) > 1 {
		size, err = strconv.ParseInt(entries[1], 10, 64)
		if err != nil {
			return -1, -1, err
		}

		size -= base
		size++
	}

	return base, size, nil
}
