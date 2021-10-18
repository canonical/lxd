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
	"github.com/lxc/lxd/lxd/ip"
	"github.com/lxc/lxd/lxd/network"
	"github.com/lxc/lxd/lxd/network/acl"
	"github.com/lxc/lxd/lxd/network/openvswitch"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/resources"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
)

// ovnNet defines an interface for accessing instance specific functions on OVN network.
type ovnNet interface {
	network.Network

	InstanceDevicePortValidateExternalRoutes(deviceInstance instance.Instance, deviceName string, externalRoutes []*net.IPNet) error
	InstanceDevicePortSetup(opts *network.OVNInstanceNICSetupOpts, securityACLsRemove []string) (openvswitch.OVNSwitchPort, error)
	InstanceDevicePortDelete(ovsExternalOVNPort openvswitch.OVNSwitchPort, opts *network.OVNInstanceNICStopOpts) error
	InstanceDevicePortDynamicIPs(instanceUUID string, deviceName string) ([]net.IP, error)
}

type nicOVN struct {
	deviceCommon

	network ovnNet // Populated in validateConfig().
}

// CanMigrate returns whether the device can be migrated to any other cluster member.
func (d *nicOVN) CanMigrate() bool {
	return true
}

// UpdatableFields returns a list of fields that can be updated without triggering a device remove & add.
func (d *nicOVN) UpdatableFields(oldDevice Type) []string {
	// Check old and new device types match.
	_, match := oldDevice.(*nicOVN)
	if !match {
		return []string{}
	}

	return []string{"security.acls"}
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
		"ipv4.routes",
		"ipv6.routes",
		"ipv4.routes.external",
		"ipv6.routes.external",
		"boot.priority",
		"security.acls",
		"security.acls.default.ingress.action",
		"security.acls.default.egress.action",
		"security.acls.default.ingress.logged",
		"security.acls.default.egress.logged",
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

	if n.Status() != api.NetworkStatusCreated {
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

	ovnNet, ok := n.(ovnNet)
	if !ok {
		return fmt.Errorf("Network is not ovnNet interface type")
	}

	d.network = ovnNet // Stored loaded network for use by other functions.
	netConfig := d.network.Config()

	if d.config["ipv4.address"] != "" {
		// Check that DHCPv4 is enabled on parent network (needed to use static assigned IPs).
		if n.DHCPv4Subnet() == nil {
			return fmt.Errorf("Cannot specify %q when DHCP is disabled on network %q", "ipv4.address", d.config["network"])
		}

		ip, subnet, err := net.ParseCIDR(netConfig["ipv4.address"])
		if err != nil {
			return errors.Wrapf(err, "Invalid network ipv4.address")
		}

		// Check the static IP supplied is valid for the linked network. It should be part of the
		// network's subnet, but not necessarily part of the dynamic allocation ranges.
		if !dhcpalloc.DHCPValidIP(subnet, nil, net.ParseIP(d.config["ipv4.address"])) {
			return fmt.Errorf("Device IP address %q not within network %q subnet", d.config["ipv4.address"], d.config["network"])
		}

		// IP should not be the same as the parent managed network address.
		if ip.Equal(net.ParseIP(d.config["ipv4.address"])) {
			return fmt.Errorf("IP address %q is assigned to parent managed network device %q", d.config["ipv4.address"], d.config["parent"])
		}
	}

	if d.config["ipv6.address"] != "" {
		// Check that DHCPv6 is enabled on parent network (needed to use static assigned IPs).
		if n.DHCPv6Subnet() == nil || !shared.IsTrue(netConfig["ipv6.dhcp.stateful"]) {
			return fmt.Errorf("Cannot specify %q when DHCP or %q are disabled on network %q", "ipv6.address", "ipv6.dhcp.stateful", d.config["network"])
		}

		ip, subnet, err := net.ParseCIDR(netConfig["ipv6.address"])
		if err != nil {
			return errors.Wrapf(err, "Invalid network ipv6.address")
		}

		// Check the static IP supplied is valid for the linked network. It should be part of the
		// network's subnet, but not necessarily part of the dynamic allocation ranges.
		if !dhcpalloc.DHCPValidIP(subnet, nil, net.ParseIP(d.config["ipv6.address"])) {
			return fmt.Errorf("Device IP address %q not within network %q subnet", d.config["ipv6.address"], d.config["network"])
		}

		// IP should not be the same as the parent managed network address.
		if ip.Equal(net.ParseIP(d.config["ipv6.address"])) {
			return fmt.Errorf("IP address %q is assigned to parent managed network device %q", d.config["ipv6.address"], d.config["parent"])
		}
	}

	// Apply network level config options to device config before validation.
	d.config["mtu"] = fmt.Sprintf("%s", netConfig["bridge.mtu"])

	rules := nicValidationRules(requiredFields, optionalFields, instConf)

	// Now run normal validation.
	err = d.config.Validate(rules)
	if err != nil {
		return err
	}

	// Check IP external routes are within the network's external routes.
	var externalRoutes []*net.IPNet
	for _, k := range []string{"ipv4.routes.external", "ipv6.routes.external"} {
		if d.config[k] == "" {
			continue
		}

		externalRoutes, err = network.SubnetParseAppend(externalRoutes, util.SplitNTrimSpace(d.config[k], ",", -1, false)...)
		if err != nil {
			return err
		}
	}

	if len(externalRoutes) > 0 {
		err = d.network.InstanceDevicePortValidateExternalRoutes(d.inst, d.name, externalRoutes)
		if err != nil {
			return err
		}
	}

	// Check Security ACLs exist.
	if d.config["security.acls"] != "" {
		err = acl.Exists(d.state, networkProjectName, util.SplitNTrimSpace(d.config["security.acls"], ",", -1, true)...)
		if err != nil {
			return err
		}
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

	// Load uplink network config.
	uplinkNetworkName := d.network.Config()["network"]
	_, uplink, _, err := d.state.Cluster.GetNetworkInAnyState(project.Default, uplinkNetworkName)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to load uplink network %q", uplinkNetworkName)
	}

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

	// Add new OVN logical switch port for instance.
	logicalPortName, err := d.network.InstanceDevicePortSetup(&network.OVNInstanceNICSetupOpts{
		InstanceUUID: d.inst.LocalConfig()["volatile.uuid"],
		DNSName:      d.inst.Name(),
		DeviceName:   d.name,
		DeviceConfig: d.config,
		UplinkConfig: uplink.Config,
	}, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed setting up OVN port")
	}

	revert.Add(func() {
		d.network.InstanceDevicePortDelete("", &network.OVNInstanceNICStopOpts{
			InstanceUUID: d.inst.LocalConfig()["volatile.uuid"],
			DeviceName:   d.name,
			DeviceConfig: d.config,
		})
	})

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
	runConf.PostHooks = []func() error{d.postStart}
	runConf.NetworkInterface = []deviceConfig.RunConfigItem{
		{Key: "type", Value: "phys"},
		{Key: "name", Value: d.config["name"]},
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

// postStart is run after the device is added to the instance.
func (d *nicOVN) postStart() error {
	err := bgpAddPrefix(&d.deviceCommon, d.network, d.config)
	if err != nil {
		return err
	}

	return nil
}

// Update applies configuration changes to a started device.
func (d *nicOVN) Update(oldDevices deviceConfig.Devices, isRunning bool) error {
	oldConfig := oldDevices[d.name]

	// Populate device config with volatile fields if needed.
	networkVethFillFromVolatile(d.config, d.volatileGet())

	// If an IPv6 address has changed, if the instance is running we should bounce the host-side
	// veth interface to give the instance a chance to detect the change and re-apply for an
	// updated lease with new IP address.
	if d.config["ipv6.address"] != oldConfig["ipv6.address"] && d.config["host_name"] != "" && network.InterfaceExists(d.config["host_name"]) {
		link := &ip.Link{Name: d.config["host_name"]}
		err := link.SetDown()
		if err != nil {
			return err
		}
		err = link.SetUp()
		if err != nil {
			return err
		}
	}

	// Apply any changes needed when assigned ACLs change.
	if d.config["security.acls"] != oldConfig["security.acls"] {
		// Work out which ACLs have been removed and remove logical port from those groups.
		oldACLs := util.SplitNTrimSpace(oldConfig["security.acls"], ",", -1, true)
		newACLs := util.SplitNTrimSpace(d.config["security.acls"], ",", -1, true)
		removedACLs := []string{}
		for _, oldACL := range oldACLs {
			if !shared.StringInSlice(oldACL, newACLs) {
				removedACLs = append(removedACLs, oldACL)
			}
		}

		// Setup the logical port with new ACLs if running.
		if isRunning {
			// Load uplink network config.
			uplinkNetworkName := d.network.Config()["network"]
			_, uplink, _, err := d.state.Cluster.GetNetworkInAnyState(project.Default, uplinkNetworkName)
			if err != nil {
				return errors.Wrapf(err, "Failed to load uplink network %q", uplinkNetworkName)
			}

			// Update OVN logical switch port for instance.
			_, err = d.network.InstanceDevicePortSetup(&network.OVNInstanceNICSetupOpts{
				InstanceUUID: d.inst.LocalConfig()["volatile.uuid"],
				DNSName:      d.inst.Name(),
				DeviceName:   d.name,
				DeviceConfig: d.config,
				UplinkConfig: uplink.Config,
			}, removedACLs)
			if err != nil {
				return errors.Wrapf(err, "Failed updating OVN port")
			}
		}

		if len(removedACLs) > 0 {
			client, err := openvswitch.NewOVN(d.state)
			if err != nil {
				return errors.Wrapf(err, "Failed to get OVN client")
			}

			err = acl.OVNPortGroupDeleteIfUnused(d.state, d.logger, client, d.network.Project(), d.inst, d.name, newACLs...)
			if err != nil {
				return errors.Wrapf(err, "Failed removing unused OVN port groups")
			}
		}
	}

	// If an external address changed, update the BGP advertisements.
	err := bgpRemovePrefix(&d.deviceCommon, oldConfig)
	if err != nil {
		return err
	}

	err = bgpAddPrefix(&d.deviceCommon, d.network, d.config)
	if err != nil {
		return err
	}

	return nil
}

// Stop is run when the device is removed from the instance.
func (d *nicOVN) Stop() (*deviceConfig.RunConfig, error) {
	runConf := deviceConfig.RunConfig{
		PostHooks: []func() error{d.postStop},
	}

	// Try and retrieve the last associated OVN switch port for the instance interface in the local OVS DB.
	// If we cannot get this, don't fail, as InstanceDevicePortDelete will then try and generate the likely
	// port name using the same regime it does for new ports. This part is only here in order to allow
	// instance ports generated under an older regime to be cleaned up properly.
	networkVethFillFromVolatile(d.config, d.volatileGet())
	ovs := openvswitch.NewOVS()
	ovsExternalOVNPort, err := ovs.InterfaceAssociatedOVNSwitchPort(d.config["host_name"])
	if err != nil {
		d.logger.Warn("Could not find OVN Switch port associated to OVS interface", log.Ctx{"interface": d.config["host_name"]})
	}

	instanceUUID := d.inst.LocalConfig()["volatile.uuid"]
	err = d.network.InstanceDevicePortDelete(ovsExternalOVNPort, &network.OVNInstanceNICStopOpts{
		InstanceUUID: instanceUUID,
		DeviceName:   d.name,
		DeviceConfig: d.config,
	})
	if err != nil {
		// Don't fail here as we still want the postStop hook to run to clean up the local veth pair.
		d.logger.Error("Failed to remove OVN device port", log.Ctx{"err": err})
	}

	// Remove BGP announcements.
	err = bgpRemovePrefix(&d.deviceCommon, d.config)
	if err != nil {
		return nil, err
	}

	return &runConf, nil
}

// postStop is run after the device is removed from the instance.
func (d *nicOVN) postStop() error {
	defer d.volatileSet(map[string]string{
		"host_name": "",
	})

	networkVethFillFromVolatile(d.config, d.volatileGet())

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
	// Check for port groups that will become unused (and need deleting) as this NIC is deleted.
	securityACLs := util.SplitNTrimSpace(d.config["security.acls"], ",", -1, true)
	if len(securityACLs) > 0 {
		client, err := openvswitch.NewOVN(d.state)
		if err != nil {
			return errors.Wrapf(err, "Failed to get OVN client")
		}

		err = acl.OVNPortGroupDeleteIfUnused(d.state, d.logger, client, d.network.Project(), d.inst, d.name)
		if err != nil {
			return errors.Wrapf(err, "Failed removing unused OVN port groups")
		}
	}

	return nil
}

// State gets the state of an OVN NIC by querying the OVN Northbound logical switch port record.
func (d *nicOVN) State() (*api.InstanceStateNetwork, error) {
	// Populate device config with volatile fields (hwaddr and host_name) if needed.
	networkVethFillFromVolatile(d.config, d.volatileGet())

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
		instanceUUID := d.inst.LocalConfig()["volatile.uuid"]
		dynamicIPs, err := d.network.InstanceDevicePortDynamicIPs(instanceUUID, d.name)
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

	// Get MTU of host interface that connects to OVN integration bridge if exists.
	iface, err := net.InterfaceByName(d.config["host_name"])
	if err != nil {
		d.logger.Warn("Failed getting host interface state for MTU", log.Ctx{"host_name": d.config["host_name"], "err": err})
	}

	mtu := -1
	if iface != nil {
		mtu = iface.MTU
	}

	// Retrieve the host counters, as we report the values from the instance's point of view,
	// those counters need to be reversed below.
	hostCounters, err := resources.GetNetworkCounters(d.config["host_name"])
	if err != nil {
		return nil, errors.Wrapf(err, "Failed getting network interface counters")
	}

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
		Mtu:      mtu,
		State:    "up",
		Type:     "broadcast",
	}

	return &network, nil
}

// Register sets up anything needed on LXD startup.
func (d *nicOVN) Register() error {
	err := bgpAddPrefix(&d.deviceCommon, d.network, d.config)
	if err != nil {
		return err
	}

	return nil
}
