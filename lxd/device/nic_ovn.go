package device

import (
	"fmt"
	"net"
	"os"

	"github.com/mdlayher/netx/eui64"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/cluster"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/dnsmasq/dhcpalloc"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/network/openvswitch"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
)

type nicOVN struct {
	deviceCommon

	network network.Network
}

// getIntegrationBridgeName returns the OVS integration bridge to use.
func (d *nicOVN) getIntegrationBridgeName() (string, error) {
	integrationBridge, err := cluster.ConfigGetString(d.state.Cluster, "network.ovn.integration_bridge")
	if err != nil {
		return "", errors.Wrapf(err, "Failed to get OVN integration bridge name")
	}

	return integrationBridge, nil
}

// validateConfig checks the supplied config for correctness.
func (d *nicOVN) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	requiredFields := []string{
		"network",
	}

	optionalFields := []string{
		"name",
		"hwaddr",
		"host_name",
		"mtu",
		"ipv4.address",
		"ipv6.address",
		"boot.priority",
	}

	// The NIC's network may be a non-default project, so lookup project and get network's project name.
	networkProjectName, _, err := project.NetworkProject(d.state.Cluster, instConf.Project())
	if err != nil {
		return errors.Wrapf(err, "Failed loading network project name")
	}

	// Lookup network settings and apply them to the device's config.
	n, err := network.LoadByName(d.state, networkProjectName, d.config["network"])
	if err != nil {
		return errors.Wrapf(err, "Error loading network config for %q", d.config["network"])
	}

	if n.Status() == api.NetworkStatusPending {
		return fmt.Errorf("Specified network is not fully created")
	}

	if n.Type() != "ovn" {
		return fmt.Errorf("Specified network must be of type ovn")
	}

	bannedKeys := []string{"mtu"}
	for _, bannedKey := range bannedKeys {
		if d.config[bannedKey] != "" {
			return fmt.Errorf("Cannot use %q property in conjunction with %q property", bannedKey, "network")
		}
	}

	d.network = n // Stored loaded instance for use by other functions.
	netConfig := d.network.Config()

	if d.config["ipv4.address"] != "" {
		// Check that DHCPv4 is enabled on parent network (needed to use static assigned IPs).
		if n.DHCPv4Subnet() == nil {
			return fmt.Errorf("Cannot specify %q when DHCP is disabled on network %q", "ipv4.address", d.config["network"])
		}

		_, subnet, err := net.ParseCIDR(netConfig["ipv4.address"])
		if err != nil {
			return errors.Wrapf(err, "Invalid network ipv4.address")
		}

		// Check the static IP supplied is valid for the linked network. It should be part of the
		// network's subnet, but not necessarily part of the dynamic allocation ranges.
		if !dhcpalloc.DHCPValidIP(subnet, nil, net.ParseIP(d.config["ipv4.address"])) {
			return fmt.Errorf("Device IP address %q not within network %q subnet", d.config["ipv4.address"], d.config["network"])
		}
	}

	if d.config["ipv6.address"] != "" {
		// Check that DHCPv6 is enabled on parent network (needed to use static assigned IPs).
		if n.DHCPv6Subnet() == nil || !shared.IsTrue(netConfig["ipv6.dhcp.stateful"]) {
			return fmt.Errorf("Cannot specify %q when DHCP or %q are disabled on network %q", "ipv6.address", "ipv6.dhcp.stateful", d.config["network"])
		}

		_, subnet, err := net.ParseCIDR(netConfig["ipv6.address"])
		if err != nil {
			return errors.Wrapf(err, "Invalid network ipv6.address")
		}

		// Check the static IP supplied is valid for the linked network. It should be part of the
		// network's subnet, but not necessarily part of the dynamic allocation ranges.
		if !dhcpalloc.DHCPValidIP(subnet, nil, net.ParseIP(d.config["ipv6.address"])) {
			return fmt.Errorf("Device IP address %q not within network %q subnet", d.config["ipv6.address"], d.config["network"])
		}
	}

	// Apply network level config options to device config before validation.
	d.config["mtu"] = fmt.Sprintf("%s", netConfig["bridge.mtu"])

	rules := nicValidationRules(requiredFields, optionalFields)

	// Now run normal validation.
	err = d.config.Validate(rules)
	if err != nil {
		return err
	}

	return nil
}

// validateEnvironment checks the runtime environment for correctness.
func (d *nicOVN) validateEnvironment() error {
	if d.inst.Type() == instancetype.Container && d.config["name"] == "" {
		return fmt.Errorf("Requires name property to start")
	}

	integrationBridge, err := d.getIntegrationBridgeName()
	if err != nil {
		return err
	}

	if !shared.PathExists(fmt.Sprintf("/sys/class/net/%s", integrationBridge)) {
		return fmt.Errorf("OVS integration bridge device %q doesn't exist", integrationBridge)
	}

	return nil
}

// CanHotPlug returns whether the device can be managed whilst the instance is running, it also
// returns a list of fields that can be updated without triggering a device remove & add.
func (d *nicOVN) CanHotPlug() (bool, []string) {
	return true, []string{}
}

// Add is run when a device is added to an instance whether or not the instance is running.
func (d *nicOVN) Add() error {
	return nil
}

// Start is run when the device is added to a running instance or instance is starting up.
func (d *nicOVN) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, err
	}

	revert := revert.New()
	defer revert.Fail()

	saveData := make(map[string]string)
	saveData["host_name"] = d.config["host_name"]

	var peerName string

	// Create veth pair and configure the peer end with custom hwaddr and mtu if supplied.
	if d.inst.Type() == instancetype.Container {
		if saveData["host_name"] == "" {
			saveData["host_name"] = network.RandomDevName("veth")
		}
		peerName, err = networkCreateVethPair(saveData["host_name"], d.config)
	} else if d.inst.Type() == instancetype.VM {
		if saveData["host_name"] == "" {
			saveData["host_name"] = network.RandomDevName("tap")
		}
		peerName = saveData["host_name"] // VMs use the host_name to link to the TAP FD.
		err = networkCreateTap(saveData["host_name"], d.config)
	}

	if err != nil {
		return nil, err
	}

	revert.Add(func() { network.InterfaceRemove(saveData["host_name"]) })

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, saveData)

	// Apply host-side limits.
	err = networkSetupHostVethLimits(d.config)
	if err != nil {
		return nil, err
	}

	// Disable IPv6 on host-side veth interface (prevents host-side interface getting link-local address)
	// which isn't needed because the host-side interface is connected to a bridge.
	err = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/disable_ipv6", saveData["host_name"]), "1")
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	mac, err := net.ParseMAC(d.config["hwaddr"])
	if err != nil {
		return nil, err
	}

	ips := []net.IP{}
	for _, key := range []string{"ipv4.address", "ipv6.address"} {
		if d.config[key] != "" {
			ip := net.ParseIP(d.config[key])
			if ip == nil {
				return nil, fmt.Errorf("Invalid %s value %q", key, d.config[key])
			}
			ips = append(ips, ip)
		}
	}

	// Add new OVN logical switch port for instance.
	logicalPortName, err := network.OVNInstanceDevicePortAdd(d.network, d.inst.ID(), d.inst.Name(), d.name, mac, ips)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed adding OVN port")
	}

	revert.Add(func() { network.OVNInstanceDevicePortDelete(d.network, d.inst.ID(), d.name) })

	// Attach host side veth interface to bridge.
	integrationBridge, err := d.getIntegrationBridgeName()
	if err != nil {
		return nil, err
	}

	ovs := openvswitch.NewOVS()
	err = ovs.BridgePortAdd(integrationBridge, saveData["host_name"], true)
	if err != nil {
		return nil, err
	}

	revert.Add(func() { ovs.BridgePortDelete(integrationBridge, saveData["host_name"]) })

	// Link OVS port to OVN logical port.
	err = ovs.InterfaceAssociateOVNSwitchPort(saveData["host_name"], logicalPortName)
	if err != nil {
		return nil, err
	}

	// Attempt to disable router advertisement acceptance.
	err = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/accept_ra", saveData["host_name"]), "0")
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Attempt to disable IPv4 forwarding.
	err = util.SysctlSet(fmt.Sprintf("net/ipv4/conf/%s/forwarding", saveData["host_name"]), "0")
	if err != nil {
		return nil, err
	}

	err = d.volatileSet(saveData)
	if err != nil {
		return nil, err
	}

	runConf := deviceConfig.RunConfig{}
	runConf.NetworkInterface = []deviceConfig.RunConfigItem{
		{Key: "name", Value: d.config["name"]},
		{Key: "type", Value: "phys"},
		{Key: "flags", Value: "up"},
		{Key: "link", Value: peerName},
	}

	if d.inst.Type() == instancetype.VM {
		runConf.NetworkInterface = append(runConf.NetworkInterface,
			[]deviceConfig.RunConfigItem{
				{Key: "devName", Value: d.name},
				{Key: "hwaddr", Value: d.config["hwaddr"]},
			}...)
	}

	revert.Success()
	return &runConf, nil
}

// Update applies configuration changes to a started device.
func (d *nicOVN) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	oldConfig := oldDevices[d.name]

	v := d.volatileGet()

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, v)

	// If instance is running, apply host side limits and filters first before rebuilding
	// dnsmasq config below so that existing config can be used as part of the filter removal.
	if isRunning {
		err := d.validateEnvironment()
		if err != nil {
			return err
		}

		// Apply host-side limits.
		err = networkSetupHostVethLimits(d.config)
		if err != nil {
			return err
		}
	}

	// If an IPv6 address has changed, if the instance is running we should bounce the host-side
	// veth interface to give the instance a chance to detect the change and re-apply for an
	// updated lease with new IP address.
	if d.config["ipv6.address"] != oldConfig["ipv6.address"] && d.config["host_name"] != "" && shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["host_name"])) {
		_, err := shared.RunCommand("ip", "link", "set", d.config["host_name"], "down")
		if err != nil {
			return err
		}
		_, err = shared.RunCommand("ip", "link", "set", d.config["host_name"], "up")
		if err != nil {
			return err
		}
	}

	return nil
}

// Stop is run when the device is removed from the instance.
func (d *nicOVN) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	err := network.OVNInstanceDevicePortDelete(d.network, d.inst.ID(), d.name)
	if err != nil {
		// Don't fail here as we still want the postStop hook to run to clean up the local veth pair.
		d.logger.Error("Failed to remove OVN device port", log.Ctx{"err": err})
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicOVN) postStop() error {
	defer d.volatileSet(map[string]string{
		"host_name": "",
	})

	v := d.volatileGet()

	networkVethFillFromVolatile(d.config, v)

	if d.config["host_name"] != "" && shared.PathExists(fmt.Sprintf("/sys/class/net/%s", d.config["host_name"])) {
		integrationBridge, err := d.getIntegrationBridgeName()
		if err != nil {
			return err
		}

		ovs := openvswitch.NewOVS()

		// Detach host-side end of veth pair from bridge (required for openvswitch particularly).
		err = ovs.BridgePortDelete(integrationBridge, d.config["host_name"])
		if err != nil {
			return errors.Wrapf(err, "Failed to detach interface %q from %q", d.config["host_name"], integrationBridge)
		}

		// Removing host-side end of veth pair will delete the peer end too.
		err = network.InterfaceRemove(d.config["host_name"])
		if err != nil {
			return errors.Wrapf(err, "Failed to remove interface %q", d.config["host_name"])
		}
	}

	return nil
}

// Remove is run when the device is removed from the instance or the instance is deleted.
func (d *nicOVN) Remove() error {
	return nil
}

// State gets the state of an OVN NIC by querying the OVN Northbound logical switch port record.
func (d *nicOVN) State() (*api.InstanceStateNetwork, error) {
	v := d.volatileGet()

	// Populate device config with volatile fields (hwaddr and host_name) if needed.
	networkVethFillFromVolatile(d.config, v)

	addresses := []api.InstanceStateNetworkAddress{}
	netConfig := d.network.Config()

	// Extract subnet sizes from bridge addresses.
	_, v4subnet, _ := net.ParseCIDR(netConfig["ipv4.address"])
	_, v6subnet, _ := net.ParseCIDR(netConfig["ipv6.address"])

	var v4mask string
	if v4subnet != nil {
		mask, _ := v4subnet.Mask.Size()
		v4mask = fmt.Sprintf("%d", mask)
	}

	var v6mask string
	if v6subnet != nil {
		mask, _ := v6subnet.Mask.Size()
		v6mask = fmt.Sprintf("%d", mask)
	}

	// OVN only supports dynamic IP allocation if neither IPv4 or IPv6 are statically set.
	if d.config["ipv4.address"] == "" && d.config["ipv6.address"] == "" {
		dynamicIPs, err := network.OVNInstanceDevicePortDynamicIPs(d.network, d.inst.ID(), d.name)
		if err == nil {
			for _, dynamicIP := range dynamicIPs {
				family := "inet"
				netmask := v4mask

				if dynamicIP.To4() == nil {
					family = "inet6"
					netmask = v6mask
				}

				addresses = append(addresses, api.InstanceStateNetworkAddress{
					Family:  family,
					Address: dynamicIP.String(),
					Netmask: netmask,
					Scope:   "global",
				})
			}
		} else {
			d.logger.Warn("Failed getting OVN port dynamic IPs", log.Ctx{"err": err})
		}
	} else {
		if d.config["ipv4.address"] != "" {
			// Static DHCPv4 allocation present, that is likely to be the NIC's IPv4. So assume that.
			addresses = append(addresses, api.InstanceStateNetworkAddress{
				Family:  "inet",
				Address: d.config["ipv4.address"],
				Netmask: v4mask,
				Scope:   "global",
			})
		}

		if d.config["ipv6.address"] != "" {
			// Static DHCPv6 allocation present, that is likely to be the NIC's IPv6. So assume that.
			addresses = append(addresses, api.InstanceStateNetworkAddress{
				Family:  "inet6",
				Address: d.config["ipv6.address"],
				Netmask: v6mask,
				Scope:   "global",
			})
		} else if !shared.IsTrue(netConfig["ipv6.dhcp.stateful"]) && d.config["hwaddr"] != "" && v6subnet != nil {
			// If no static DHCPv6 allocation and stateful DHCPv6 is disabled, and IPv6 is enabled on
			// the bridge, the the NIC is likely to use its MAC and SLAAC to configure its address.
			hwAddr, err := net.ParseMAC(d.config["hwaddr"])
			if err == nil {
				ip, err := eui64.ParseMAC(v6subnet.IP, hwAddr)
				if err == nil {
					addresses = append(addresses, api.InstanceStateNetworkAddress{
						Family:  "inet6",
						Address: ip.String(),
						Netmask: v6mask,
						Scope:   "global",
					})
				}
			}
		}
	}

	// Get MTU of host interface that connects to OVN integration bridge.
	iface, err := net.InterfaceByName(d.config["host_name"])
	if err != nil {
		return nil, errors.Wrapf(err, "Failed getting host interface state")
	}

	// Retrieve the host counters, as we report the values from the instance's point of view,
	// those counters need to be reversed below.
	hostCounters := shared.NetworkGetCounters(d.config["host_name"])
	network := api.InstanceStateNetwork{
		Addresses: addresses,
		Counters: api.InstanceStateNetworkCounters{
			BytesReceived:   hostCounters.BytesSent,
			BytesSent:       hostCounters.BytesReceived,
			PacketsReceived: hostCounters.PacketsSent,
			PacketsSent:     hostCounters.PacketsReceived,
		},
		Hwaddr:   d.config["hwaddr"],
		HostName: d.config["host_name"],
		Mtu:      iface.MTU,
		State:    "up",
		Type:     "broadcast",
	}

	return &network, nil
}
