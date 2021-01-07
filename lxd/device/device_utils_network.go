package device

import (
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"
	"sync"

	"github.com/pkg/errors"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/device/nictype"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/units"
	"github.com/lxc/lxd/shared/validate"
)

// Instances can be started in parallel, so lock the creation of VLANs.
var networkCreateSharedDeviceLock sync.Mutex

// NetworkSetDevMTU sets the MTU setting for a named network device if different from current.
func NetworkSetDevMTU(devName string, mtu uint32) error {
	curMTU, err := network.GetDevMTU(devName)
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

// networkRemoveInterfaceIfNeeded removes a network interface by name but only if no other instance is using it.
func networkRemoveInterfaceIfNeeded(state *state.State, nic string, current instance.Instance, parent string, vlanID string) error {
	// Check if it's used by another instance.
	instances, err := instance.LoadNodeAll(state, instancetype.Any)
	if err != nil {
		return err
	}

	for _, inst := range instances {
		if inst.Name() == current.Name() && inst.Project() == current.Project() {
			continue
		}

		for devName, dev := range inst.ExpandedDevices() {
			if dev["type"] != "nic" || dev["vlan"] != vlanID || dev["parent"] != parent {
				continue
			}

			// Check if another running instance created the device, if so, don't touch it.
			if shared.IsTrue(inst.ExpandedConfig()[fmt.Sprintf("volatile.%s.last_state.created", devName)]) {
				return nil
			}
		}
	}

	return network.InterfaceRemove(nic)
}

// networkCreateVlanDeviceIfNeeded creates a VLAN device if doesn't already exist.
func networkCreateVlanDeviceIfNeeded(state *state.State, parent string, vlanDevice string, vlanID string) (string, error) {
	if vlanID != "" {
		created, err := network.VLANInterfaceCreate(parent, vlanDevice, vlanID)
		if err != nil {
			return "", err
		}

		if created {
			return "created", nil
		}

		// Check if it was created for another running instance.
		instances, err := instance.LoadNodeAll(state, instancetype.Any)
		if err != nil {
			return "", err
		}

		for _, inst := range instances {
			for devName, dev := range inst.ExpandedDevices() {
				if dev["type"] != "nic" || dev["vlan"] != vlanID || dev["parent"] != parent {
					continue
				}

				// Check if another running instance created the device, if so, mark it as created.
				if shared.IsTrue(inst.ExpandedConfig()[fmt.Sprintf("volatile.%s.last_state.created", devName)]) {
					return "reused", nil
				}
			}
		}
	}

	return "existing", nil
}

// networkSnapshotPhysicalNic records properties of the NIC to volatile so they can be restored later.
func networkSnapshotPhysicalNic(hostName string, volatile map[string]string) error {
	// Store current MTU for restoration on detach.
	mtu, err := network.GetDevMTU(hostName)
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
		return network.InterfaceRemove(hostName)
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

		err = NetworkSetDevMTU(hostName, uint32(mtuInt))
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

// networkCreateVethPair creates and configures a veth pair. It will set the hwaddr and mtu settings
// in the supplied config to the newly created peer interface. If mtu is not specified, but parent
// is supplied in config, then the MTU of the new peer interface will inherit the parent MTU.
// Accepts the name of the host side interface as a parameter and returns the peer interface name.
func networkCreateVethPair(hostName string, m deviceConfig.Device) (string, error) {
	peerName := network.RandomDevName("veth")

	_, err := shared.RunCommand("ip", "link", "add", "dev", hostName, "type", "veth", "peer", "name", peerName)
	if err != nil {
		return "", fmt.Errorf("Failed to create the veth interfaces %s and %s: %v", hostName, peerName, err)
	}

	_, err = shared.RunCommand("ip", "link", "set", "dev", hostName, "up")
	if err != nil {
		network.InterfaceRemove(hostName)
		return "", fmt.Errorf("Failed to bring up the veth interface %s: %v", hostName, err)
	}

	// Set the MAC address on peer.
	if m["hwaddr"] != "" {
		_, err := shared.RunCommand("ip", "link", "set", "dev", peerName, "address", m["hwaddr"])
		if err != nil {
			network.InterfaceRemove(peerName)
			return "", fmt.Errorf("Failed to set the MAC address: %v", err)
		}
	}

	// Set the MTU on peer. If not specified and has parent, will inherit MTU from parent.
	if m["mtu"] != "" {
		mtu, err := strconv.ParseUint(m["mtu"], 10, 32)
		if err != nil {
			return "", fmt.Errorf("Invalid MTU specified: %v", err)
		}

		err = NetworkSetDevMTU(peerName, uint32(mtu))
		if err != nil {
			network.InterfaceRemove(peerName)
			return "", fmt.Errorf("Failed to set the MTU: %v", err)
		}

		err = NetworkSetDevMTU(hostName, uint32(mtu))
		if err != nil {
			network.InterfaceRemove(peerName)
			return "", fmt.Errorf("Failed to set the MTU: %v", err)
		}
	} else if m["parent"] != "" {
		parentMTU, err := network.GetDevMTU(m["parent"])
		if err != nil {
			return "", fmt.Errorf("Failed to get the parent MTU: %v", err)
		}

		err = NetworkSetDevMTU(peerName, parentMTU)
		if err != nil {
			network.InterfaceRemove(peerName)
			return "", fmt.Errorf("Failed to set the MTU: %v", err)
		}

		err = NetworkSetDevMTU(hostName, parentMTU)
		if err != nil {
			network.InterfaceRemove(peerName)
			return "", fmt.Errorf("Failed to set the MTU: %v", err)
		}
	}

	return peerName, nil
}

// networkCreateTap creates and configures a TAP device.
func networkCreateTap(hostName string, m deviceConfig.Device) error {
	_, err := shared.RunCommand("ip", "tuntap", "add", "name", hostName, "mode", "tap", "multi_queue")
	if err != nil {
		return errors.Wrapf(err, "Failed to create the tap interfaces %s", hostName)
	}

	revert := revert.New()
	defer revert.Fail()

	_, err = shared.RunCommand("ip", "link", "set", "dev", hostName, "up")
	if err != nil {
		return errors.Wrapf(err, "Failed to bring up the tap interface %s", hostName)
	}
	revert.Add(func() { network.InterfaceRemove(hostName) })

	// Set the MTU on peer. If not specified and has parent, will inherit MTU from parent.
	if m["mtu"] != "" {
		mtu, err := strconv.ParseUint(m["mtu"], 10, 32)
		if err != nil {
			return errors.Wrap(err, "Invalid MTU specified")
		}

		err = NetworkSetDevMTU(hostName, uint32(mtu))
		if err != nil {
			return errors.Wrap(err, "Failed to set the MTU")
		}
	} else if m["parent"] != "" {
		parentMTU, err := network.GetDevMTU(m["parent"])
		if err != nil {
			return errors.Wrap(err, "Failed to get the parent MTU")
		}

		err = NetworkSetDevMTU(hostName, parentMTU)
		if err != nil {
			return errors.Wrap(err, "Failed to set the MTU")
		}
	}

	revert.Success()
	return nil
}

// networkSetupHostVethRoutes configures a nic device's host side veth routes.
// Accepts an optional oldDevice that will have its old host routes removed before adding the new device routes.
// This allows live update of a veth device.
func networkSetupHostVethRoutes(s *state.State, device deviceConfig.Device, oldDevice deviceConfig.Device, v map[string]string) error {
	// Check whether host device resolution succeeded.
	if device["host_name"] == "" {
		return fmt.Errorf("Failed to find host side veth name for device %q", device["name"])
	}

	// If oldDevice provided, remove old routes if any remain.
	if oldDevice != nil {
		networkVethFillFromVolatile(oldDevice, v)
		networkRemoveVethRoutes(s, oldDevice)
	}

	// Setup static routes to container.
	err := networkSetVethRoutes(s, device)
	if err != nil {
		return err
	}

	return nil
}

// networkVethFillFromVolatile fills veth host_name and hwaddr fields from volatile if not set in device config.
func networkVethFillFromVolatile(device deviceConfig.Device, volatile map[string]string) {
	// If not configured, check if volatile data contains the most recently added host_name.
	if device["host_name"] == "" {
		device["host_name"] = volatile["host_name"]
	}

	// If not configured, check if volatile data contains the most recently added hwaddr.
	if device["hwaddr"] == "" {
		device["hwaddr"] = volatile["hwaddr"]
	}
}

// networkSetVethRoutes applies any static routes configured from the host to the container nic.
func networkSetVethRoutes(s *state.State, m deviceConfig.Device) error {
	// Decide whether the route should point to the veth parent or the bridge parent.
	routeDev := m["host_name"]

	// Use project.Default here, as only networks in the default project can add routes on the host.
	nicType, err := nictype.NICType(s, project.Default, m)
	if err != nil {
		return err
	}

	if nicType == "bridged" {
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
func networkRemoveVethRoutes(s *state.State, m deviceConfig.Device) {
	// Decide whether the route should point to the veth parent or the bridge parent
	routeDev := m["host_name"]

	// Use project.Default here, as only networks in the default project can add routes on the host.
	nicType, err := nictype.NICType(s, project.Default, m)
	if err != nil {
		logger.Errorf("Failed to get NIC type for %q", m["name"])
		return
	}

	if nicType == "bridged" {
		routeDev = m["parent"]
	}

	if m["ipv4.routes"] != "" || m["ipv6.routes"] != "" {
		if routeDev == "" {
			logger.Errorf("Failed to remove static routes as route dev isn't set for %q", m["name"])
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

// networkSetupHostVethLimits applies any network rate limits to the veth device specified in the config.
func networkSetupHostVethLimits(m deviceConfig.Device) error {
	var err error

	veth := m["host_name"]

	if veth == "" || !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", veth)) {
		return fmt.Errorf("Unknown or missing host side veth device %q", veth)
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
		_, err := shared.RunCommand("tc", "qdisc", "add", "dev", veth, "root", "handle", "1:0", "htb", "default", "10")
		if err != nil {
			return fmt.Errorf("Failed to create root tc qdisc: %s", err)
		}

		_, err = shared.RunCommand("tc", "class", "add", "dev", veth, "parent", "1:0", "classid", "1:10", "htb", "rate", fmt.Sprintf("%dbit", ingressInt))
		if err != nil {
			return fmt.Errorf("Failed to create limit tc class: %s", err)
		}

		_, err = shared.RunCommand("tc", "filter", "add", "dev", veth, "parent", "1:0", "protocol", "all", "u32", "match", "u32", "0", "0", "flowid", "1:1")
		if err != nil {
			return fmt.Errorf("Failed to create tc filter: %s", err)
		}
	}

	if m["limits.egress"] != "" {
		_, err := shared.RunCommand("tc", "qdisc", "add", "dev", veth, "handle", "ffff:0", "ingress")
		if err != nil {
			return fmt.Errorf("Failed to create ingress tc qdisc: %s", err)
		}

		_, err = shared.RunCommand("tc", "filter", "add", "dev", veth, "parent", "ffff:0", "protocol", "all", "u32", "match", "u32", "0", "0", "police", "rate", fmt.Sprintf("%dbit", egressInt), "burst", "1024k", "mtu", "64kb", "drop")
		if err != nil {
			return fmt.Errorf("Failed to create ingress tc qdisc: %s", err)
		}
	}

	return nil
}

// networkValidGateway validates the gateway value.
func networkValidGateway(value string) error {
	if shared.StringInSlice(value, []string{"none", "auto"}) {
		return nil
	}

	return fmt.Errorf("Invalid gateway: %s", value)
}

// networkValidVLANList validates a comma delimited list of VLAN IDs.
func networkValidVLANList(value string) error {
	for _, vlanID := range strings.Split(value, ",") {
		vlanID = strings.TrimSpace(vlanID)
		err := validate.IsNetworkVLAN(vlanID)
		if err != nil {
			return err
		}
	}

	return nil
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
