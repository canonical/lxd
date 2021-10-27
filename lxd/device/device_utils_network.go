package device

import (
	"fmt"
	"io/ioutil"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/pkg/errors"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/ip"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/network/openvswitch"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
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
		link := &ip.Link{Name: devName}
		err := link.SetMTU(fmt.Sprintf("%d", mtu))
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
		link := &ip.Link{Name: devName}
		err := link.SetAddress(mac)
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
func networkCreateVlanDeviceIfNeeded(state *state.State, parent string, vlanDevice string, vlanID string, gvrp bool) (string, error) {
	if vlanID != "" {
		created, err := network.VLANInterfaceCreate(parent, vlanDevice, vlanID, gvrp)
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
	link := &ip.Link{Name: hostName}
	err := link.SetDown()
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

	veth := &ip.Veth{
		Link: ip.Link{
			Name: hostName,
		},
		PeerName: peerName,
	}
	err := veth.Add()
	if err != nil {
		return "", fmt.Errorf("Failed to create the veth interfaces %s and %s: %v", hostName, peerName, err)
	}

	err = veth.SetUp()
	if err != nil {
		network.InterfaceRemove(hostName)
		return "", fmt.Errorf("Failed to bring up the veth interface %s: %v", hostName, err)
	}

	// Set the MAC address on peer.
	if m["hwaddr"] != "" {
		link := &ip.Link{Name: peerName}
		err := link.SetAddress(m["hwaddr"])
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
	tuntap := &ip.Tuntap{
		Name:       hostName,
		Mode:       "tap",
		MultiQueue: true,
	}
	err := tuntap.Add()
	if err != nil {
		return errors.Wrapf(err, "Failed to create the tap interfaces %s", hostName)
	}

	revert := revert.New()
	defer revert.Fail()

	link := &ip.Link{Name: hostName}
	err = link.SetUp()
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

// networkNICRouteAdd applies any static host-side routes configured for an instance NIC.
func networkNICRouteAdd(routeDev string, routes ...string) error {
	if !network.InterfaceExists(routeDev) {
		return fmt.Errorf("Route interface missing %q", routeDev)
	}

	revert := revert.New()
	defer revert.Fail()

	for _, r := range routes {
		route := r // Local var for revert.
		ipAddress, _, err := net.ParseCIDR(route)
		if err != nil {
			return errors.Wrapf(err, "Invalid route %q", route)
		}

		ipVersion := ip.FamilyV4
		if ipAddress.To4() == nil {
			ipVersion = ip.FamilyV6
		}

		// Add IP route (using boot proto to avoid conflicts with network defined static routes).
		r := &ip.Route{
			DevName: routeDev,
			Route:   route,
			Proto:   "boot",
			Family:  ipVersion,
		}
		err = r.Add()
		if err != nil {
			return err
		}

		revert.Add(func() {
			r := &ip.Route{
				DevName: routeDev,
				Route:   route,
				Proto:   "boot",
				Family:  ipVersion,
			}
			r.Flush()
		})
	}

	revert.Success()
	return nil
}

// networkNICRouteDelete deletes any static host-side routes configured for an instance NIC.
// Logs any errors and continues to next route to remove.
func networkNICRouteDelete(routeDev string, routes ...string) {
	if routeDev == "" {
		logger.Errorf("Failed removing static route, empty route device specified")
		return
	}

	if !network.InterfaceExists(routeDev) {
		return //Routes will already be gone if device doesn't exist.
	}

	for _, r := range routes {
		route := r // Local var for revert.
		ipAddress, _, err := net.ParseCIDR(route)
		if err != nil {
			logger.Errorf("Failed to remove static route %q to %q: %v", route, routeDev, err)
			continue
		}

		ipVersion := ip.FamilyV4
		if ipAddress.To4() == nil {
			ipVersion = ip.FamilyV6
		}

		// Add IP route (using boot proto to avoid conflicts with network defined static routes).
		r := &ip.Route{
			DevName: routeDev,
			Route:   route,
			Proto:   "boot",
			Family:  ipVersion,
		}
		r.Flush()
		if err != nil {
			logger.Errorf("Failed to remove static route %q to %q: %v", route, routeDev, err)
			continue
		}
	}
}

// networkSetupHostVethLimits applies any network rate limits to the veth device specified in the config.
func networkSetupHostVethLimits(m deviceConfig.Device) error {
	var err error

	veth := m["host_name"]

	if veth == "" || !network.InterfaceExists(veth) {
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
	qdisc := &ip.Qdisc{Dev: veth, Root: true}
	qdisc.Delete()
	qdisc = &ip.Qdisc{Dev: veth, Ingress: true}
	qdisc.Delete()

	// Apply new limits
	if m["limits.ingress"] != "" {
		qdiscHTB := &ip.QdiscHTB{Qdisc: ip.Qdisc{Dev: veth, Handle: "1:0", Root: true}, Default: "10"}
		err := qdiscHTB.Add()
		if err != nil {
			return fmt.Errorf("Failed to create root tc qdisc: %s", err)
		}

		classHTB := &ip.ClassHTB{Class: ip.Class{Dev: veth, Parent: "1:0", Classid: "1:10"}, Rate: fmt.Sprintf("%dbit", ingressInt)}
		err = classHTB.Add()
		if err != nil {
			return fmt.Errorf("Failed to create limit tc class: %s", err)
		}

		filter := &ip.U32Filter{Filter: ip.Filter{Dev: veth, Parent: "1:0", Protocol: "all", Flowid: "1:1"}, Value: "0", Mask: "0"}
		err = filter.Add()
		if err != nil {
			return fmt.Errorf("Failed to create tc filter: %s", err)
		}
	}

	if m["limits.egress"] != "" {
		qdisc = &ip.Qdisc{Dev: veth, Handle: "ffff:0", Ingress: true}
		err := qdisc.Add()
		if err != nil {
			return fmt.Errorf("Failed to create ingress tc qdisc: %s", err)
		}

		police := &ip.ActionPolice{Rate: fmt.Sprintf("%dbit", egressInt), Burst: "1024k", Mtu: "64kb", Drop: true}
		filter := &ip.U32Filter{Filter: ip.Filter{Dev: veth, Parent: "ffff:0", Protocol: "all"}, Value: "0", Mask: "0", Actions: []ip.Action{police}}
		err = filter.Add()
		if err != nil {
			return fmt.Errorf("Failed to create ingress tc filter: %s", err)
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

// bgpAddPrefix adds external routes to the BGP server.
func bgpAddPrefix(d *deviceCommon, n network.Network, config map[string]string) error {
	// BGP is only valid when tied to a managed network.
	if config["network"] == "" {
		return nil
	}

	// Parse nexthop configuration.
	nexthopV4 := net.ParseIP(n.Config()["bgp.ipv4.nexthop"])
	if nexthopV4 == nil {
		nexthopV4 = net.ParseIP(n.Config()["volatile.network.ipv4.address"])
		if nexthopV4 == nil {
			nexthopV4 = net.ParseIP("0.0.0.0")
		}
	}

	nexthopV6 := net.ParseIP(n.Config()["bgp.ipv6.nexthop"])
	if nexthopV6 == nil {
		nexthopV6 = net.ParseIP(n.Config()["volatile.network.ipv6.address"])
		if nexthopV6 == nil {
			nexthopV6 = net.ParseIP("::")
		}
	}

	// Add the prefixes.
	bgpOwner := fmt.Sprintf("instance_%d_%s", d.inst.ID(), d.name)
	if config["ipv4.routes.external"] != "" {
		for _, prefix := range util.SplitNTrimSpace(config["ipv4.routes.external"], ",", -1, true) {
			_, prefixNet, err := net.ParseCIDR(prefix)
			if err != nil {
				return err
			}

			err = d.state.BGP.AddPrefix(*prefixNet, nexthopV4, bgpOwner)
			if err != nil {
				return err
			}
		}
	}

	if config["ipv6.routes.external"] != "" {
		for _, prefix := range util.SplitNTrimSpace(config["ipv6.routes.external"], ",", -1, true) {
			_, prefixNet, err := net.ParseCIDR(prefix)
			if err != nil {
				return err
			}

			err = d.state.BGP.AddPrefix(*prefixNet, nexthopV6, bgpOwner)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func bgpRemovePrefix(d *deviceCommon, config map[string]string) error {
	// BGP is only valid when tied to a managed network.
	if config["network"] == "" {
		return nil
	}

	// Load the network configuration.
	err := d.state.BGP.RemovePrefixByOwner(fmt.Sprintf("instance_%d_%s", d.inst.ID(), d.name))
	if err != nil {
		return err
	}

	return nil
}

func networkValidAcceleration(value string) error {
	err := validate.IsOneOf("none", "sriov")(value)
	if err != nil {
		return err
	}

	if value == "sriov" {
		ovs := openvswitch.NewOVS()

		if !ovs.HardwareOffloadingEnabled() {
			return errors.New("acceleration=sriov cannot be used if hardware offloading is disabled")
		}
	}

	return nil
}
