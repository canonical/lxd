package device

import (
	"fmt"
	"net"
	"os"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/cluster"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/dnsmasq/dhcpalloc"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/network/openvswitch"
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

	// Lookup network settings and apply them to the device's config.
	n, err := network.LoadByName(d.state, d.config["network"])
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
	mtu, err := network.OVNInstanceDeviceMTU(n)
	if err != nil {
		return err
	}

	d.config["mtu"] = fmt.Sprintf("%d", mtu)

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
			saveData["host_name"] = networkRandomDevName("veth")
		}
		peerName, err = networkCreateVethPair(saveData["host_name"], d.config)
	} else if d.inst.Type() == instancetype.VM {
		if saveData["host_name"] == "" {
			saveData["host_name"] = networkRandomDevName("tap")
		}
		peerName = saveData["host_name"] // VMs use the host_name to link to the TAP FD.
		err = networkCreateTap(saveData["host_name"], d.config)
	}

	if err != nil {
		return nil, err
	}

	revert.Add(func() { NetworkRemoveInterface(saveData["host_name"]) })

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
		return nil, err
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
		err = NetworkRemoveInterface(d.config["host_name"])
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
