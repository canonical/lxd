package network

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/daemon"
	"github.com/lxc/lxd/lxd/dnsmasq"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/subprocess"
	"github.com/lxc/lxd/shared/version"
)

// ForkdnsServersListPath defines the path that contains the forkdns server candidate file.
const ForkdnsServersListPath = "forkdns.servers"

// ForkdnsServersListFile file that contains the server candidates list.
const ForkdnsServersListFile = "servers.conf"

var forkdnsServersLock sync.Mutex

// bridge represents a LXD bridge network.
type bridge struct {
	common
}

// Validate network config.
func (n *bridge) Validate(config map[string]string) error {
	// Build driver specific rules dynamically.
	rules := map[string]func(value string) error{
		"bridge.driver": func(value string) error {
			return shared.IsOneOf(value, []string{"native", "openvswitch"})
		},
		"bridge.external_interfaces": func(value string) error {
			if value == "" {
				return nil
			}

			for _, entry := range strings.Split(value, ",") {
				entry = strings.TrimSpace(entry)
				if ValidNetworkName(entry) != nil {
					return fmt.Errorf("Invalid interface name '%s'", entry)
				}
			}

			return nil
		},
		"bridge.hwaddr": shared.IsAny,
		"bridge.mtu":    shared.IsInt64,
		"bridge.mode": func(value string) error {
			return shared.IsOneOf(value, []string{"standard", "fan"})
		},

		"fan.overlay_subnet": shared.IsNetworkV4,
		"fan.underlay_subnet": func(value string) error {
			if value == "auto" {
				return nil
			}

			return shared.IsNetworkV4(value)
		},
		"fan.type": func(value string) error {
			return shared.IsOneOf(value, []string{"vxlan", "ipip"})
		},

		"ipv4.address": func(value string) error {
			if shared.IsOneOf(value, []string{"none", "auto"}) == nil {
				return nil
			}

			return shared.IsNetworkAddressCIDRV4(value)
		},
		"ipv4.firewall": shared.IsBool,
		"ipv4.nat":      shared.IsBool,
		"ipv4.nat.order": func(value string) error {
			return shared.IsOneOf(value, []string{"before", "after"})
		},
		"ipv4.nat.address":  shared.IsNetworkAddressV4,
		"ipv4.dhcp":         shared.IsBool,
		"ipv4.dhcp.gateway": shared.IsNetworkAddressV4,
		"ipv4.dhcp.expiry":  shared.IsAny,
		"ipv4.dhcp.ranges":  shared.IsAny,
		"ipv4.routes":       shared.IsNetworkV4List,
		"ipv4.routing":      shared.IsBool,

		"ipv6.address": func(value string) error {
			if shared.IsOneOf(value, []string{"none", "auto"}) == nil {
				return nil
			}

			return shared.IsNetworkAddressCIDRV6(value)
		},
		"ipv6.firewall": shared.IsBool,
		"ipv6.nat":      shared.IsBool,
		"ipv6.nat.order": func(value string) error {
			return shared.IsOneOf(value, []string{"before", "after"})
		},
		"ipv6.nat.address":   shared.IsNetworkAddressV6,
		"ipv6.dhcp":          shared.IsBool,
		"ipv6.dhcp.expiry":   shared.IsAny,
		"ipv6.dhcp.stateful": shared.IsBool,
		"ipv6.dhcp.ranges":   shared.IsAny,
		"ipv6.routes":        shared.IsNetworkV6List,
		"ipv6.routing":       shared.IsBool,

		"dns.domain": shared.IsAny,
		"dns.mode": func(value string) error {
			return shared.IsOneOf(value, []string{"dynamic", "managed", "none"})
		},

		"raw.dnsmasq": shared.IsAny,

		"maas.subnet.ipv4": shared.IsAny,
		"maas.subnet.ipv6": shared.IsAny,
	}

	// Add dynamic validation rules.
	for k := range config {
		// Tunnel keys have the remote name in their name, so extract the real key
		if strings.HasPrefix(k, "tunnel.") {
			// Validate remote name in key.
			fields := strings.Split(k, ".")
			if len(fields) != 3 {
				return fmt.Errorf("Invalid network configuration key: %s", k)
			}

			if len(n.name)+len(fields[1]) > 14 {
				return fmt.Errorf("Network name too long for tunnel interface: %s-%s", n.name, fields[1])
			}

			tunnelKey := fields[2]

			// Add the correct validation rule for the dynamic field based on last part of key.
			switch tunnelKey {
			case "protocol":
				rules[k] = func(value string) error {
					return shared.IsOneOf(value, []string{"gre", "vxlan"})
				}
			case "local":
				rules[k] = shared.IsNetworkAddress
			case "remote":
				rules[k] = shared.IsNetworkAddress
			case "port":
				rules[k] = networkValidPort
			case "group":
				rules[k] = shared.IsNetworkAddress
			case "id":
				rules[k] = shared.IsInt64
			case "inteface":
				rules[k] = ValidNetworkName
			case "ttl":
				rules[k] = shared.IsUint8
			}
		}
	}

	err := n.validate(config, rules)
	if err != nil {
		return err
	}

	// Peform composite key checks after per-key validation.

	// Validate network name when used in fan mode.
	bridgeMode := config["bridge.mode"]
	if bridgeMode == "fan" && len(n.name) > 11 {
		return fmt.Errorf("Network name too long to use with the FAN (must be 11 characters or less)")
	}

	for k, v := range config {
		key := k
		// Bridge mode checks
		if bridgeMode == "fan" && strings.HasPrefix(key, "ipv4.") && !shared.StringInSlice(key, []string{"ipv4.dhcp.expiry", "ipv4.firewall", "ipv4.nat", "ipv4.nat.order"}) && v != "" {
			return fmt.Errorf("IPv4 configuration may not be set when in 'fan' mode")
		}

		if bridgeMode == "fan" && strings.HasPrefix(key, "ipv6.") && v != "" {
			return fmt.Errorf("IPv6 configuration may not be set when in 'fan' mode")
		}

		if bridgeMode != "fan" && strings.HasPrefix(key, "fan.") && v != "" {
			return fmt.Errorf("FAN configuration may only be set when in 'fan' mode")
		}

		// MTU checks
		if key == "bridge.mtu" && v != "" {
			mtu, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("Invalid value for an integer: %s", v)
			}

			ipv6 := config["ipv6.address"]
			if ipv6 != "" && ipv6 != "none" && mtu < 1280 {
				return fmt.Errorf("The minimum MTU for an IPv6 network is 1280")
			}

			ipv4 := config["ipv4.address"]
			if ipv4 != "" && ipv4 != "none" && mtu < 68 {
				return fmt.Errorf("The minimum MTU for an IPv4 network is 68")
			}

			if config["bridge.mode"] == "fan" {
				if config["fan.type"] == "ipip" {
					if mtu > 1480 {
						return fmt.Errorf("Maximum MTU for an IPIP FAN bridge is 1480")
					}
				} else {
					if mtu > 1450 {
						return fmt.Errorf("Maximum MTU for a VXLAN FAN bridge is 1450")
					}
				}
			}
		}
	}

	return nil
}

// isRunning returns whether the network is up.
func (n *bridge) isRunning() bool {
	return shared.PathExists(fmt.Sprintf("/sys/class/net/%s", n.name))
}

// Delete deletes a network.
func (n *bridge) Delete(clusterNotification bool) error {
	n.logger.Debug("Delete", log.Ctx{"clusterNotification": clusterNotification})

	// Bring the network down.
	if n.isRunning() {
		err := n.Stop()
		if err != nil {
			return err
		}
	}

	return n.common.delete(clusterNotification)
}

// Rename renames a network.
func (n *bridge) Rename(newName string) error {
	n.logger.Debug("Rename", log.Ctx{"newName": newName})

	// Sanity checks.
	if n.IsUsed() {
		return fmt.Errorf("The network is currently in use")
	}

	// Bring the network down.
	if n.isRunning() {
		err := n.Stop()
		if err != nil {
			return err
		}
	}

	// Rename forkdns log file.
	forkDNSLogPath := fmt.Sprintf("forkdns.%s.log", n.name)
	if shared.PathExists(shared.LogPath(forkDNSLogPath)) {
		err := os.Rename(forkDNSLogPath, shared.LogPath(fmt.Sprintf("forkdns.%s.log", newName)))
		if err != nil {
			return err
		}
	}

	// Rename common steps.
	err := n.common.rename(newName)
	if err != nil {
		return err
	}

	// Bring the network up.
	err = n.Start()
	if err != nil {
		return err
	}

	return nil
}

// Start starts the network.
func (n *bridge) Start() error {
	return n.setup(nil)
}

// setup restarts the network.
func (n *bridge) setup(oldConfig map[string]string) error {
	// If we are in mock mode, just no-op.
	if n.state.OS.MockMode {
		return nil
	}

	n.logger.Debug("Setting up network")

	// Create directory
	if !shared.PathExists(shared.VarPath("networks", n.name)) {
		err := os.MkdirAll(shared.VarPath("networks", n.name), 0711)
		if err != nil {
			return err
		}
	}

	// Create the bridge interface
	if !n.isRunning() {
		if n.config["bridge.driver"] == "openvswitch" {
			_, err := exec.LookPath("ovs-vsctl")
			if err != nil {
				return fmt.Errorf("Open vSwitch isn't installed on this system")
			}

			_, err = shared.RunCommand("ovs-vsctl", "add-br", n.name)
			if err != nil {
				return err
			}
		} else {
			_, err := shared.RunCommand("ip", "link", "add", "dev", n.name, "type", "bridge")
			if err != nil {
				return err
			}
		}
	}

	// Get a list of tunnels
	tunnels := n.getTunnels()

	// IPv6 bridge configuration
	if !shared.StringInSlice(n.config["ipv6.address"], []string{"", "none"}) {
		if !shared.PathExists("/proc/sys/net/ipv6") {
			return fmt.Errorf("Network has ipv6.address but kernel IPv6 support is missing")
		}

		err := util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/autoconf", n.name), "0")
		if err != nil {
			return err
		}

		err = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/accept_dad", n.name), "0")
		if err != nil {
			return err
		}
	}

	// Get a list of interfaces
	ifaces, err := net.Interfaces()
	if err != nil {
		return err
	}

	// Cleanup any existing tunnel device
	for _, iface := range ifaces {
		if strings.HasPrefix(iface.Name, fmt.Sprintf("%s-", n.name)) {
			_, err = shared.RunCommand("ip", "link", "del", "dev", iface.Name)
			if err != nil {
				return err
			}
		}
	}

	// Set the MTU
	mtu := ""
	if n.config["bridge.mtu"] != "" {
		mtu = n.config["bridge.mtu"]
	} else if len(tunnels) > 0 {
		mtu = "1400"
	} else if n.config["bridge.mode"] == "fan" {
		if n.config["fan.type"] == "ipip" {
			mtu = "1480"
		} else {
			mtu = "1450"
		}
	}

	// Attempt to add a dummy device to the bridge to force the MTU
	if mtu != "" && n.config["bridge.driver"] != "openvswitch" {
		_, err = shared.RunCommand("ip", "link", "add", "dev", fmt.Sprintf("%s-mtu", n.name), "mtu", mtu, "type", "dummy")
		if err == nil {
			_, err = shared.RunCommand("ip", "link", "set", "dev", fmt.Sprintf("%s-mtu", n.name), "up")
			if err == nil {
				AttachInterface(n.name, fmt.Sprintf("%s-mtu", n.name))
			}
		}
	}

	// Now, set a default MTU
	if mtu == "" {
		mtu = "1500"
	}

	_, err = shared.RunCommand("ip", "link", "set", "dev", n.name, "mtu", mtu)
	if err != nil {
		return err
	}

	// Set the MAC address
	if n.config["bridge.hwaddr"] != "" {
		_, err = shared.RunCommand("ip", "link", "set", "dev", n.name, "address", n.config["bridge.hwaddr"])
		if err != nil {
			return err
		}
	}

	// Bring it up
	_, err = shared.RunCommand("ip", "link", "set", "dev", n.name, "up")
	if err != nil {
		return err
	}

	// Add any listed existing external interface
	if n.config["bridge.external_interfaces"] != "" {
		for _, entry := range strings.Split(n.config["bridge.external_interfaces"], ",") {
			entry = strings.TrimSpace(entry)
			iface, err := net.InterfaceByName(entry)
			if err != nil {
				n.logger.Warn("Skipping attaching missing external interface", log.Ctx{"interface": entry})
				continue
			}

			unused := true
			addrs, err := iface.Addrs()
			if err == nil {
				for _, addr := range addrs {
					ip, _, err := net.ParseCIDR(addr.String())
					if ip != nil && err == nil && ip.IsGlobalUnicast() {
						unused = false
						break
					}
				}
			}

			if !unused {
				return fmt.Errorf("Only unconfigured network interfaces can be bridged")
			}

			err = AttachInterface(n.name, entry)
			if err != nil {
				return err
			}
		}
	}

	// Remove any existing IPv4 firewall rules.
	if usesIPv4Firewall(n.config) || usesIPv4Firewall(oldConfig) {
		err = n.state.Firewall.NetworkClear(n.name, 4)
		if err != nil {
			return err
		}
	}

	// Snapshot container specific IPv4 routes (added with boot proto) before removing IPv4 addresses.
	// This is because the kernel removes any static routes on an interface when all addresses removed.
	ctRoutes, err := n.bootRoutesV4()
	if err != nil {
		return err
	}

	// Flush all IPv4 addresses and routes
	_, err = shared.RunCommand("ip", "-4", "addr", "flush", "dev", n.name, "scope", "global")
	if err != nil {
		return err
	}

	_, err = shared.RunCommand("ip", "-4", "route", "flush", "dev", n.name, "proto", "static")
	if err != nil {
		return err
	}

	// Configure IPv4 firewall (includes fan)
	if n.config["bridge.mode"] == "fan" || !shared.StringInSlice(n.config["ipv4.address"], []string{"", "none"}) {
		if n.HasDHCPv4() && n.HasIPv4Firewall() {
			// Setup basic iptables overrides for DHCP/DNS
			err = n.state.Firewall.NetworkSetupDHCPDNSAccess(n.name, 4)
			if err != nil {
				return err
			}
		}

		// Attempt a workaround for broken DHCP clients
		if n.HasIPv4Firewall() {
			err = n.state.Firewall.NetworkSetupDHCPv4Checksum(n.name)
			if err != nil {
				return err
			}
		}

		// Allow forwarding
		if n.config["bridge.mode"] == "fan" || n.config["ipv4.routing"] == "" || shared.IsTrue(n.config["ipv4.routing"]) {
			err = util.SysctlSet("net/ipv4/ip_forward", "1")
			if err != nil {
				return err
			}

			if n.HasIPv4Firewall() {
				err = n.state.Firewall.NetworkSetupForwardingPolicy(n.name, 4, true)
				if err != nil {
					return err
				}
			}
		} else {
			if n.HasIPv4Firewall() {
				err = n.state.Firewall.NetworkSetupForwardingPolicy(n.name, 4, false)
				if err != nil {
					return err
				}
			}
		}
	}

	// Start building process using subprocess package
	command := "dnsmasq"
	dnsmasqCmd := []string{"--keep-in-foreground", "--strict-order", "--bind-interfaces",
		"--except-interface=lo",
		"--no-ping", // --no-ping is very important to prevent delays to lease file updates.
		fmt.Sprintf("--interface=%s", n.name)}

	dnsmasqVersion, err := dnsmasq.GetVersion()
	if err != nil {
		return err
	}

	// --dhcp-rapid-commit option is only supported on >2.79
	minVer, _ := version.NewDottedVersion("2.79")
	if dnsmasqVersion.Compare(minVer) > 0 {
		dnsmasqCmd = append(dnsmasqCmd, "--dhcp-rapid-commit")
	}

	if !daemon.Debug {
		// --quiet options are only supported on >2.67
		minVer, _ := version.NewDottedVersion("2.67")

		if err == nil && dnsmasqVersion.Compare(minVer) > 0 {
			dnsmasqCmd = append(dnsmasqCmd, []string{"--quiet-dhcp", "--quiet-dhcp6", "--quiet-ra"}...)
		}
	}

	// Configure IPv4
	if !shared.StringInSlice(n.config["ipv4.address"], []string{"", "none"}) {
		// Parse the subnet
		ip, subnet, err := net.ParseCIDR(n.config["ipv4.address"])
		if err != nil {
			return err
		}

		// Update the dnsmasq config
		dnsmasqCmd = append(dnsmasqCmd, fmt.Sprintf("--listen-address=%s", ip.String()))
		if n.HasDHCPv4() {
			if !shared.StringInSlice("--dhcp-no-override", dnsmasqCmd) {
				dnsmasqCmd = append(dnsmasqCmd, []string{"--dhcp-no-override", "--dhcp-authoritative", fmt.Sprintf("--dhcp-leasefile=%s", shared.VarPath("networks", n.name, "dnsmasq.leases")), fmt.Sprintf("--dhcp-hostsfile=%s", shared.VarPath("networks", n.name, "dnsmasq.hosts"))}...)
			}

			if n.config["ipv4.dhcp.gateway"] != "" {
				dnsmasqCmd = append(dnsmasqCmd, fmt.Sprintf("--dhcp-option-force=3,%s", n.config["ipv4.dhcp.gateway"]))
			}

			if mtu != "1500" {
				dnsmasqCmd = append(dnsmasqCmd, fmt.Sprintf("--dhcp-option-force=26,%s", mtu))
			}

			expiry := "1h"
			if n.config["ipv4.dhcp.expiry"] != "" {
				expiry = n.config["ipv4.dhcp.expiry"]
			}

			if n.config["ipv4.dhcp.ranges"] != "" {
				for _, dhcpRange := range strings.Split(n.config["ipv4.dhcp.ranges"], ",") {
					dhcpRange = strings.TrimSpace(dhcpRange)
					dnsmasqCmd = append(dnsmasqCmd, []string{"--dhcp-range", fmt.Sprintf("%s,%s", strings.Replace(dhcpRange, "-", ",", -1), expiry)}...)
				}
			} else {
				dnsmasqCmd = append(dnsmasqCmd, []string{"--dhcp-range", fmt.Sprintf("%s,%s,%s", GetIP(subnet, 2).String(), GetIP(subnet, -2).String(), expiry)}...)
			}
		}

		// Add the address
		_, err = shared.RunCommand("ip", "-4", "addr", "add", "dev", n.name, n.config["ipv4.address"])
		if err != nil {
			return err
		}

		// Configure NAT
		if shared.IsTrue(n.config["ipv4.nat"]) {
			//If a SNAT source address is specified, use that, otherwise default to using MASQUERADE mode.
			var srcIP net.IP
			if n.config["ipv4.nat.address"] != "" {
				srcIP = net.ParseIP(n.config["ipv4.nat.address"])
			}

			if n.config["ipv4.nat.order"] == "after" {
				err = n.state.Firewall.NetworkSetupOutboundNAT(n.name, subnet, srcIP, true)
				if err != nil {
					return err
				}
			} else {
				err = n.state.Firewall.NetworkSetupOutboundNAT(n.name, subnet, srcIP, false)
				if err != nil {
					return err
				}
			}
		}

		// Add additional routes
		if n.config["ipv4.routes"] != "" {
			for _, route := range strings.Split(n.config["ipv4.routes"], ",") {
				route = strings.TrimSpace(route)
				_, err = shared.RunCommand("ip", "-4", "route", "add", "dev", n.name, route, "proto", "static")
				if err != nil {
					return err
				}
			}
		}

		// Restore container specific IPv4 routes to interface.
		err = n.applyBootRoutesV4(ctRoutes)
		if err != nil {
			return err
		}
	}

	// Remove any existing IPv6 firewall rules.
	if usesIPv6Firewall(n.config) || usesIPv6Firewall(oldConfig) {
		err = n.state.Firewall.NetworkClear(n.name, 6)
		if err != nil {
			return err
		}
	}

	// Snapshot container specific IPv6 routes (added with boot proto) before removing IPv6 addresses.
	// This is because the kernel removes any static routes on an interface when all addresses removed.
	ctRoutes, err = n.bootRoutesV6()
	if err != nil {
		return err
	}

	// Flush all IPv6 addresses and routes
	_, err = shared.RunCommand("ip", "-6", "addr", "flush", "dev", n.name, "scope", "global")
	if err != nil {
		return err
	}

	_, err = shared.RunCommand("ip", "-6", "route", "flush", "dev", n.name, "proto", "static")
	if err != nil {
		return err
	}

	// Configure IPv6
	if !shared.StringInSlice(n.config["ipv6.address"], []string{"", "none"}) {
		// Enable IPv6 for the subnet
		err := util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/disable_ipv6", n.name), "0")
		if err != nil {
			return err
		}

		// Parse the subnet
		ip, subnet, err := net.ParseCIDR(n.config["ipv6.address"])
		if err != nil {
			return err
		}
		subnetSize, _ := subnet.Mask.Size()

		if subnetSize > 64 {
			n.logger.Warn("IPv6 networks with a prefix larger than 64 aren't properly supported by dnsmasq")
		}

		// Update the dnsmasq config
		dnsmasqCmd = append(dnsmasqCmd, []string{fmt.Sprintf("--listen-address=%s", ip.String()), "--enable-ra"}...)
		if n.HasDHCPv6() {
			if n.config["ipv6.firewall"] == "" || shared.IsTrue(n.config["ipv6.firewall"]) {
				// Setup basic iptables overrides for DHCP/DNS
				err = n.state.Firewall.NetworkSetupDHCPDNSAccess(n.name, 6)
				if err != nil {
					return err
				}
			}

			// Build DHCP configuration
			if !shared.StringInSlice("--dhcp-no-override", dnsmasqCmd) {
				dnsmasqCmd = append(dnsmasqCmd, []string{"--dhcp-no-override", "--dhcp-authoritative", fmt.Sprintf("--dhcp-leasefile=%s", shared.VarPath("networks", n.name, "dnsmasq.leases")), fmt.Sprintf("--dhcp-hostsfile=%s", shared.VarPath("networks", n.name, "dnsmasq.hosts"))}...)
			}

			expiry := "1h"
			if n.config["ipv6.dhcp.expiry"] != "" {
				expiry = n.config["ipv6.dhcp.expiry"]
			}

			if shared.IsTrue(n.config["ipv6.dhcp.stateful"]) {
				if n.config["ipv6.dhcp.ranges"] != "" {
					for _, dhcpRange := range strings.Split(n.config["ipv6.dhcp.ranges"], ",") {
						dhcpRange = strings.TrimSpace(dhcpRange)
						dnsmasqCmd = append(dnsmasqCmd, []string{"--dhcp-range", fmt.Sprintf("%s,%d,%s", strings.Replace(dhcpRange, "-", ",", -1), subnetSize, expiry)}...)
					}
				} else {
					dnsmasqCmd = append(dnsmasqCmd, []string{"--dhcp-range", fmt.Sprintf("%s,%s,%d,%s", GetIP(subnet, 2), GetIP(subnet, -1), subnetSize, expiry)}...)
				}
			} else {
				dnsmasqCmd = append(dnsmasqCmd, []string{"--dhcp-range", fmt.Sprintf("::,constructor:%s,ra-stateless,ra-names", n.name)}...)
			}
		} else {
			dnsmasqCmd = append(dnsmasqCmd, []string{"--dhcp-range", fmt.Sprintf("::,constructor:%s,ra-only", n.name)}...)
		}

		// Allow forwarding
		if n.config["ipv6.routing"] == "" || shared.IsTrue(n.config["ipv6.routing"]) {
			// Get a list of proc entries
			entries, err := ioutil.ReadDir("/proc/sys/net/ipv6/conf/")
			if err != nil {
				return err
			}

			// First set accept_ra to 2 for everything
			for _, entry := range entries {
				content, err := ioutil.ReadFile(fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/accept_ra", entry.Name()))
				if err == nil && string(content) != "1\n" {
					continue
				}

				err = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/accept_ra", entry.Name()), "2")
				if err != nil && !os.IsNotExist(err) {
					return err
				}
			}

			// Then set forwarding for all of them
			for _, entry := range entries {
				err = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/forwarding", entry.Name()), "1")
				if err != nil && !os.IsNotExist(err) {
					return err
				}
			}

			if n.config["ipv6.firewall"] == "" || shared.IsTrue(n.config["ipv6.firewall"]) {
				err = n.state.Firewall.NetworkSetupForwardingPolicy(n.name, 6, true)
				if err != nil {
					return err
				}
			}
		} else {
			if n.config["ipv6.firewall"] == "" || shared.IsTrue(n.config["ipv6.firewall"]) {
				err = n.state.Firewall.NetworkSetupForwardingPolicy(n.name, 6, false)
				if err != nil {
					return err
				}
			}
		}

		// Add the address
		_, err = shared.RunCommand("ip", "-6", "addr", "add", "dev", n.name, n.config["ipv6.address"])
		if err != nil {
			return err
		}

		// Configure NAT
		if shared.IsTrue(n.config["ipv6.nat"]) {
			var srcIP net.IP
			if n.config["ipv6.nat.address"] != "" {
				srcIP = net.ParseIP(n.config["ipv6.nat.address"])
			}

			if n.config["ipv6.nat.order"] == "after" {
				err = n.state.Firewall.NetworkSetupOutboundNAT(n.name, subnet, srcIP, true)
				if err != nil {
					return err
				}
			} else {
				err = n.state.Firewall.NetworkSetupOutboundNAT(n.name, subnet, srcIP, false)
				if err != nil {
					return err
				}
			}
		}

		// Add additional routes
		if n.config["ipv6.routes"] != "" {
			for _, route := range strings.Split(n.config["ipv6.routes"], ",") {
				route = strings.TrimSpace(route)
				_, err = shared.RunCommand("ip", "-6", "route", "add", "dev", n.name, route, "proto", "static")
				if err != nil {
					return err
				}
			}
		}

		// Restore container specific IPv6 routes to interface.
		err = n.applyBootRoutesV6(ctRoutes)
		if err != nil {
			return err
		}
	}

	// Configure the fan
	dnsClustered := false
	dnsClusteredAddress := ""
	var overlaySubnet *net.IPNet
	if n.config["bridge.mode"] == "fan" {
		tunName := fmt.Sprintf("%s-fan", n.name)

		// Parse the underlay
		underlay := n.config["fan.underlay_subnet"]
		_, underlaySubnet, err := net.ParseCIDR(underlay)
		if err != nil {
			return nil
		}

		// Parse the overlay
		overlay := n.config["fan.overlay_subnet"]
		if overlay == "" {
			overlay = "240.0.0.0/8"
		}

		_, overlaySubnet, err = net.ParseCIDR(overlay)
		if err != nil {
			return err
		}

		// Get the address
		fanAddress, devName, devAddr, err := n.fanAddress(underlaySubnet, overlaySubnet)
		if err != nil {
			return err
		}

		addr := strings.Split(fanAddress, "/")
		if n.config["fan.type"] == "ipip" {
			fanAddress = fmt.Sprintf("%s/24", addr[0])
		}

		// Update the MTU based on overlay device (if available)
		fanMtuInt, err := GetDevMTU(devName)
		if err == nil {
			// Apply overhead
			if n.config["fan.type"] == "ipip" {
				fanMtuInt = fanMtuInt - 20
			} else {
				fanMtuInt = fanMtuInt - 50
			}

			// Apply changes
			fanMtu := fmt.Sprintf("%d", fanMtuInt)
			if fanMtu != mtu {
				mtu = fanMtu
				if n.config["bridge.driver"] != "openvswitch" {
					_, err = shared.RunCommand("ip", "link", "set", "dev", fmt.Sprintf("%s-mtu", n.name), "mtu", mtu)
					if err != nil {
						return err
					}
				}

				_, err = shared.RunCommand("ip", "link", "set", "dev", n.name, "mtu", mtu)
				if err != nil {
					return err
				}
			}
		}

		// Parse the host subnet
		_, hostSubnet, err := net.ParseCIDR(fmt.Sprintf("%s/24", addr[0]))
		if err != nil {
			return err
		}

		// Add the address
		_, err = shared.RunCommand("ip", "-4", "addr", "add", "dev", n.name, fanAddress)
		if err != nil {
			return err
		}

		// Update the dnsmasq config
		expiry := "1h"
		if n.config["ipv4.dhcp.expiry"] != "" {
			expiry = n.config["ipv4.dhcp.expiry"]
		}

		dnsmasqCmd = append(dnsmasqCmd, []string{
			fmt.Sprintf("--listen-address=%s", addr[0]),
			"--dhcp-no-override", "--dhcp-authoritative",
			fmt.Sprintf("--dhcp-leasefile=%s", shared.VarPath("networks", n.name, "dnsmasq.leases")),
			fmt.Sprintf("--dhcp-hostsfile=%s", shared.VarPath("networks", n.name, "dnsmasq.hosts")),
			"--dhcp-range", fmt.Sprintf("%s,%s,%s", GetIP(hostSubnet, 2).String(), GetIP(hostSubnet, -2).String(), expiry)}...)

		// Setup the tunnel
		if n.config["fan.type"] == "ipip" {
			_, err = shared.RunCommand("ip", "-4", "route", "flush", "dev", "tunl0")
			if err != nil {
				return err
			}

			_, err = shared.RunCommand("ip", "link", "set", "dev", "tunl0", "up")
			if err != nil {
				return err
			}

			// Fails if the map is already set
			shared.RunCommand("ip", "link", "change", "dev", "tunl0", "type", "ipip", "fan-map", fmt.Sprintf("%s:%s", overlay, underlay))

			_, err = shared.RunCommand("ip", "route", "add", overlay, "dev", "tunl0", "src", addr[0])
			if err != nil {
				return err
			}
		} else {
			vxlanID := fmt.Sprintf("%d", binary.BigEndian.Uint32(overlaySubnet.IP.To4())>>8)

			_, err = shared.RunCommand("ip", "link", "add", tunName, "type", "vxlan", "id", vxlanID, "dev", devName, "dstport", "0", "local", devAddr, "fan-map", fmt.Sprintf("%s:%s", overlay, underlay))
			if err != nil {
				return err
			}

			err = AttachInterface(n.name, tunName)
			if err != nil {
				return err
			}

			_, err = shared.RunCommand("ip", "link", "set", "dev", tunName, "mtu", mtu, "up")
			if err != nil {
				return err
			}

			_, err = shared.RunCommand("ip", "link", "set", "dev", n.name, "up")
			if err != nil {
				return err
			}
		}

		// Configure NAT
		if n.config["ipv4.nat"] == "" || shared.IsTrue(n.config["ipv4.nat"]) {
			if n.config["ipv4.nat.order"] == "after" {
				err = n.state.Firewall.NetworkSetupOutboundNAT(n.name, overlaySubnet, nil, true)
				if err != nil {
					return err
				}
			} else {
				err = n.state.Firewall.NetworkSetupOutboundNAT(n.name, overlaySubnet, nil, false)
				if err != nil {
					return err
				}
			}
		}

		// Setup clustered DNS
		clusterAddress, err := node.ClusterAddress(n.state.Node)
		if err != nil {
			return err
		}

		// If clusterAddress is non-empty, this indicates the intention for this node to be
		// part of a cluster and so we should ensure that dnsmasq and forkdns are started
		// in cluster mode. Note: During LXD initialisation the cluster may not actually be
		// setup yet, but we want the DNS processes to be ready for when it is.
		if clusterAddress != "" {
			dnsClustered = true
		}

		dnsClusteredAddress = strings.Split(fanAddress, "/")[0]
	}

	// Configure tunnels
	for _, tunnel := range tunnels {
		getConfig := func(key string) string {
			return n.config[fmt.Sprintf("tunnel.%s.%s", tunnel, key)]
		}

		tunProtocol := getConfig("protocol")
		tunLocal := getConfig("local")
		tunRemote := getConfig("remote")
		tunName := fmt.Sprintf("%s-%s", n.name, tunnel)

		// Configure the tunnel
		cmd := []string{"ip", "link", "add", "dev", tunName}
		if tunProtocol == "gre" {
			// Skip partial configs
			if tunProtocol == "" || tunLocal == "" || tunRemote == "" {
				continue
			}

			cmd = append(cmd, []string{"type", "gretap", "local", tunLocal, "remote", tunRemote}...)
		} else if tunProtocol == "vxlan" {
			tunGroup := getConfig("group")
			tunInterface := getConfig("interface")

			// Skip partial configs
			if tunProtocol == "" {
				continue
			}

			cmd = append(cmd, []string{"type", "vxlan"}...)

			if tunLocal != "" && tunRemote != "" {
				cmd = append(cmd, []string{"local", tunLocal, "remote", tunRemote}...)
			} else {
				if tunGroup == "" {
					tunGroup = "239.0.0.1"
				}

				devName := tunInterface
				if devName == "" {
					_, devName, err = DefaultGatewaySubnetV4()
					if err != nil {
						return err
					}
				}

				cmd = append(cmd, []string{"group", tunGroup, "dev", devName}...)
			}

			tunPort := getConfig("port")
			if tunPort == "" {
				tunPort = "0"
			}
			cmd = append(cmd, []string{"dstport", tunPort}...)

			tunID := getConfig("id")
			if tunID == "" {
				tunID = "1"
			}
			cmd = append(cmd, []string{"id", tunID}...)

			tunTTL := getConfig("ttl")
			if tunTTL == "" {
				tunTTL = "1"
			}
			cmd = append(cmd, []string{"ttl", tunTTL}...)
		}

		// Create the interface
		_, err = shared.RunCommand(cmd[0], cmd[1:]...)
		if err != nil {
			return err
		}

		// Bridge it and bring up
		err = AttachInterface(n.name, tunName)
		if err != nil {
			return err
		}

		_, err = shared.RunCommand("ip", "link", "set", "dev", tunName, "mtu", mtu, "up")
		if err != nil {
			return err
		}

		_, err = shared.RunCommand("ip", "link", "set", "dev", n.name, "up")
		if err != nil {
			return err
		}
	}

	// Kill any existing dnsmasq and forkdns daemon for this network
	err = dnsmasq.Kill(n.name, false)
	if err != nil {
		return err
	}

	err = n.killForkDNS()
	if err != nil {
		return err
	}

	// Configure dnsmasq
	if n.config["bridge.mode"] == "fan" || !shared.StringInSlice(n.config["ipv4.address"], []string{"", "none"}) || !shared.StringInSlice(n.config["ipv6.address"], []string{"", "none"}) {
		// Setup the dnsmasq domain
		dnsDomain := n.config["dns.domain"]
		if dnsDomain == "" {
			dnsDomain = "lxd"
		}

		if n.config["dns.mode"] != "none" {
			if dnsClustered {
				dnsmasqCmd = append(dnsmasqCmd, "-s", dnsDomain)
				dnsmasqCmd = append(dnsmasqCmd, "-S", fmt.Sprintf("/%s/%s#1053", dnsDomain, dnsClusteredAddress))
				dnsmasqCmd = append(dnsmasqCmd, fmt.Sprintf("--rev-server=%s,%s#1053", overlaySubnet, dnsClusteredAddress))
			} else {
				dnsmasqCmd = append(dnsmasqCmd, []string{"-s", dnsDomain, "-S", fmt.Sprintf("/%s/", dnsDomain)}...)
			}
		}

		// Create a config file to contain additional config (and to prevent dnsmasq from reading /etc/dnsmasq.conf)
		err = ioutil.WriteFile(shared.VarPath("networks", n.name, "dnsmasq.raw"), []byte(fmt.Sprintf("%s\n", n.config["raw.dnsmasq"])), 0644)
		if err != nil {
			return err
		}
		dnsmasqCmd = append(dnsmasqCmd, fmt.Sprintf("--conf-file=%s", shared.VarPath("networks", n.name, "dnsmasq.raw")))

		// Attempt to drop privileges
		if n.state.OS.UnprivUser != "" {
			dnsmasqCmd = append(dnsmasqCmd, []string{"-u", n.state.OS.UnprivUser}...)
		}

		// Create DHCP hosts directory
		if !shared.PathExists(shared.VarPath("networks", n.name, "dnsmasq.hosts")) {
			err = os.MkdirAll(shared.VarPath("networks", n.name, "dnsmasq.hosts"), 0755)
			if err != nil {
				return err
			}
		}

		// Check for dnsmasq
		_, err := exec.LookPath("dnsmasq")
		if err != nil {
			return fmt.Errorf("dnsmasq is required for LXD managed bridges")
		}

		// Update the static leases
		err = UpdateDNSMasqStatic(n.state, n.name)
		if err != nil {
			return err
		}

		// Create subprocess object dnsmasq (occasionally races, try a few times)
		p, err := subprocess.NewProcess(command, dnsmasqCmd, "", "")
		if err != nil {
			return fmt.Errorf("Failed to create subprocess: %s", err)
		}

		err = p.Start()
		if err != nil {
			return fmt.Errorf("Failed to run: %s %s: %v", command, strings.Join(dnsmasqCmd, " "), err)
		}

		err = p.Save(shared.VarPath("networks", n.name, "dnsmasq.pid"))
		if err != nil {
			// Kill Process if started, but could not save the file
			err2 := p.Stop()
			if err != nil {
				return fmt.Errorf("Could not kill subprocess while handling saving error: %s: %s", err, err2)
			}

			return fmt.Errorf("Failed to save subprocess details: %s", err)
		}

		// Spawn DNS forwarder if needed (backgrounded to avoid deadlocks during cluster boot)
		if dnsClustered {
			// Create forkdns servers directory
			if !shared.PathExists(shared.VarPath("networks", n.name, ForkdnsServersListPath)) {
				err = os.MkdirAll(shared.VarPath("networks", n.name, ForkdnsServersListPath), 0755)
				if err != nil {
					return err
				}
			}

			// Create forkdns servers.conf file if doesn't exist
			f, err := os.OpenFile(shared.VarPath("networks", n.name, ForkdnsServersListPath+"/"+ForkdnsServersListFile), os.O_RDONLY|os.O_CREATE, 0666)
			if err != nil {
				return err
			}
			f.Close()

			err = n.spawnForkDNS(dnsClusteredAddress)
			if err != nil {
				return err
			}
		}
	} else {
		// Clean up old dnsmasq config if exists and we are not starting dnsmasq.
		leasesPath := shared.VarPath("networks", n.name, "dnsmasq.leases")
		if shared.PathExists(leasesPath) {
			err := os.Remove(leasesPath)
			if err != nil {
				return errors.Wrapf(err, "Failed to remove old dnsmasq leases file '%s'", leasesPath)
			}
		}

		// And same for our PID file.
		pidPath := shared.VarPath("networks", n.name, "dnsmasq.pid")
		if shared.PathExists(pidPath) {
			err := os.Remove(pidPath)
			if err != nil {
				return errors.Wrapf(err, "Failed to remove old dnsmasq pid file '%s'", pidPath)
			}
		}
	}

	return nil
}

// Stop stops the network.
func (n *bridge) Stop() error {
	if !n.isRunning() {
		return nil
	}

	// Destroy the bridge interface
	if n.config["bridge.driver"] == "openvswitch" {
		_, err := shared.RunCommand("ovs-vsctl", "del-br", n.name)
		if err != nil {
			return err
		}
	} else {
		_, err := shared.RunCommand("ip", "link", "del", "dev", n.name)
		if err != nil {
			return err
		}
	}

	// Cleanup firewall rules.
	if usesIPv4Firewall(n.config) {
		err := n.state.Firewall.NetworkClear(n.name, 4)
		if err != nil {
			return err
		}
	}

	if usesIPv6Firewall(n.config) {
		err := n.state.Firewall.NetworkClear(n.name, 6)
		if err != nil {
			return err
		}
	}

	// Kill any existing dnsmasq and forkdns daemon for this network
	err := dnsmasq.Kill(n.name, false)
	if err != nil {
		return err
	}

	err = n.killForkDNS()
	if err != nil {
		return err
	}

	// Get a list of interfaces
	ifaces, err := net.Interfaces()
	if err != nil {
		return err
	}

	// Cleanup any existing tunnel device
	for _, iface := range ifaces {
		if strings.HasPrefix(iface.Name, fmt.Sprintf("%s-", n.name)) {
			_, err = shared.RunCommand("ip", "link", "del", "dev", iface.Name)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// Update updates the network. Accepts notification boolean indicating if this update request is coming from a
// cluster notification, in which case do not update the database, just apply local changes needed.
func (n *bridge) Update(newNetwork api.NetworkPut, clusterNotification bool) error {
	n.logger.Debug("Update", log.Ctx{"clusterNotification": clusterNotification})

	// When switching to a fan bridge, auto-detect the underlay if not specified.
	if newNetwork.Config["bridge.mode"] == "fan" {
		if newNetwork.Config["fan.underlay_subnet"] == "" {
			newNetwork.Config["fan.underlay_subnet"] = "auto"
		}
	}

	// Populate auto fields.
	err := fillAuto(newNetwork.Config)
	if err != nil {
		return err
	}

	dbUpdateNeeeded, changedKeys, oldNetwork, err := n.common.configChanged(newNetwork)
	if err != nil {
		return err
	}

	if !dbUpdateNeeeded {
		return nil // Nothing changed.
	}

	revert := revert.New()
	defer revert.Fail()

	// Define a function which reverts everything.
	revert.Add(func() {
		// Reset changes to all nodes and database.
		n.common.update(oldNetwork, clusterNotification)

		// Reset any change that was made to local bridge.
		n.setup(newNetwork.Config)
	})

	// Bring the bridge down entirely if the driver has changed.
	if shared.StringInSlice("bridge.driver", changedKeys) && n.isRunning() {
		err = n.Stop()
		if err != nil {
			return err
		}
	}

	// Detach any external interfaces should no longer be attached.
	if shared.StringInSlice("bridge.external_interfaces", changedKeys) && n.isRunning() {
		devices := []string{}
		for _, dev := range strings.Split(newNetwork.Config["bridge.external_interfaces"], ",") {
			dev = strings.TrimSpace(dev)
			devices = append(devices, dev)
		}

		for _, dev := range strings.Split(oldNetwork.Config["bridge.external_interfaces"], ",") {
			dev = strings.TrimSpace(dev)
			if dev == "" {
				continue
			}

			if !shared.StringInSlice(dev, devices) && shared.PathExists(fmt.Sprintf("/sys/class/net/%s", dev)) {
				err = DetachInterface(n.name, dev)
				if err != nil {
					return err
				}
			}
		}
	}

	// Apply changes to database.
	err = n.common.update(newNetwork, clusterNotification)
	if err != nil {
		return err
	}

	// Restart the network if needed.
	if len(changedKeys) > 0 {
		err = n.setup(oldNetwork.Config)
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

func (n *bridge) spawnForkDNS(listenAddress string) error {
	// Setup the dnsmasq domain
	dnsDomain := n.config["dns.domain"]
	if dnsDomain == "" {
		dnsDomain = "lxd"
	}

	// Spawn the daemon using subprocess
	command := n.state.OS.ExecPath
	forkdnsargs := []string{"forkdns",
		fmt.Sprintf("%s:1053", listenAddress),
		dnsDomain,
		n.name}

	logPath := shared.LogPath(fmt.Sprintf("forkdns.%s.log", n.name))

	p, err := subprocess.NewProcess(command, forkdnsargs, logPath, logPath)
	if err != nil {
		return fmt.Errorf("Failed to create subprocess: %s", err)
	}

	err = p.Start()
	if err != nil {
		return fmt.Errorf("Failed to run: %s %s: %v", command, strings.Join(forkdnsargs, " "), err)
	}

	err = p.Save(shared.VarPath("networks", n.name, "forkdns.pid"))
	if err != nil {
		// Kill Process if started, but could not save the file
		err2 := p.Stop()
		if err != nil {
			return fmt.Errorf("Could not kill subprocess while handling saving error: %s: %s", err, err2)
		}

		return fmt.Errorf("Failed to save subprocess details: %s", err)
	}

	return nil
}

// HandleHeartbeat refreshes forkdns servers. Retrieves the IPv4 address of each cluster node (excluding ourselves)
// for this network. It then updates the forkdns server list file if there are changes.
func (n *bridge) HandleHeartbeat(heartbeatData *cluster.APIHeartbeat) error {
	addresses := []string{}
	localAddress, err := node.HTTPSAddress(n.state.Node)
	if err != nil {
		return err
	}

	n.logger.Info("Refreshing forkdns peers")

	cert := n.state.Endpoints.NetworkCert()
	for _, node := range heartbeatData.Members {
		if node.Address == localAddress {
			// No need to query ourselves.
			continue
		}

		client, err := cluster.Connect(node.Address, cert, true)
		if err != nil {
			return err
		}

		state, err := client.GetNetworkState(n.name)
		if err != nil {
			return err
		}

		for _, addr := range state.Addresses {
			// Only get IPv4 addresses of nodes on network.
			if addr.Family != "inet" || addr.Scope != "global" {
				continue
			}

			addresses = append(addresses, addr.Address)
			break
		}
	}

	// Compare current stored list to retrieved list and see if we need to update.
	curList, err := ForkdnsServersList(n.name)
	if err != nil {
		// Only warn here, but continue on to regenerate the servers list from cluster info.
		n.logger.Warn("Failed to load existing forkdns server list", log.Ctx{"err": err})
	}

	// If current list is same as cluster list, nothing to do.
	if err == nil && reflect.DeepEqual(curList, addresses) {
		return nil
	}

	err = n.updateForkdnsServersFile(addresses)
	if err != nil {
		return err
	}

	n.logger.Info("Updated forkdns server list", log.Ctx{"nodes": addresses})
	return nil
}

func (n *bridge) getTunnels() []string {
	tunnels := []string{}

	for k := range n.config {
		if !strings.HasPrefix(k, "tunnel.") {
			continue
		}

		fields := strings.Split(k, ".")
		if !shared.StringInSlice(fields[1], tunnels) {
			tunnels = append(tunnels, fields[1])
		}
	}

	return tunnels
}

// bootRoutesV4 returns a list of IPv4 boot routes on the network's device.
func (n *bridge) bootRoutesV4() ([]string, error) {
	routes := []string{}
	cmd := exec.Command("ip", "-4", "route", "show", "dev", n.name, "proto", "boot")
	ipOut, err := cmd.StdoutPipe()
	if err != nil {
		return routes, err
	}
	cmd.Start()
	scanner := bufio.NewScanner(ipOut)
	for scanner.Scan() {
		route := strings.Replace(scanner.Text(), "linkdown", "", -1)
		routes = append(routes, route)
	}
	cmd.Wait()
	return routes, nil
}

// bootRoutesV6 returns a list of IPv6 boot routes on the network's device.
func (n *bridge) bootRoutesV6() ([]string, error) {
	routes := []string{}
	cmd := exec.Command("ip", "-6", "route", "show", "dev", n.name, "proto", "boot")
	ipOut, err := cmd.StdoutPipe()
	if err != nil {
		return routes, err
	}
	cmd.Start()
	scanner := bufio.NewScanner(ipOut)
	for scanner.Scan() {
		route := strings.Replace(scanner.Text(), "linkdown", "", -1)
		routes = append(routes, route)
	}
	cmd.Wait()
	return routes, nil
}

// applyBootRoutesV4 applies a list of IPv4 boot routes to the network's device.
func (n *bridge) applyBootRoutesV4(routes []string) error {
	for _, route := range routes {
		cmd := []string{"-4", "route", "replace", "dev", n.name, "proto", "boot"}
		cmd = append(cmd, strings.Fields(route)...)
		_, err := shared.RunCommand("ip", cmd...)
		if err != nil {
			return err
		}
	}

	return nil
}

// applyBootRoutesV6 applies a list of IPv6 boot routes to the network's device.
func (n *bridge) applyBootRoutesV6(routes []string) error {
	for _, route := range routes {
		cmd := []string{"-6", "route", "replace", "dev", n.name, "proto", "boot"}
		cmd = append(cmd, strings.Fields(route)...)
		_, err := shared.RunCommand("ip", cmd...)
		if err != nil {
			return err
		}
	}

	return nil
}

func (n *bridge) fanAddress(underlay *net.IPNet, overlay *net.IPNet) (string, string, string, error) {
	// Sanity checks
	underlaySize, _ := underlay.Mask.Size()
	if underlaySize != 16 && underlaySize != 24 {
		return "", "", "", fmt.Errorf("Only /16 or /24 underlays are supported at this time")
	}

	overlaySize, _ := overlay.Mask.Size()
	if overlaySize != 8 && overlaySize != 16 {
		return "", "", "", fmt.Errorf("Only /8 or /16 overlays are supported at this time")
	}

	if overlaySize+(32-underlaySize)+8 > 32 {
		return "", "", "", fmt.Errorf("Underlay or overlay networks too large to accommodate the FAN")
	}

	// Get the IP
	ip, dev, err := n.addressForSubnet(underlay)
	if err != nil {
		return "", "", "", err
	}
	ipStr := ip.String()

	// Force into IPv4 format
	ipBytes := ip.To4()
	if ipBytes == nil {
		return "", "", "", fmt.Errorf("Invalid IPv4: %s", ip)
	}

	// Compute the IP
	ipBytes[0] = overlay.IP[0]
	if overlaySize == 16 {
		ipBytes[1] = overlay.IP[1]
		ipBytes[2] = ipBytes[3]
	} else if underlaySize == 24 {
		ipBytes[1] = ipBytes[3]
		ipBytes[2] = 0
	} else if underlaySize == 16 {
		ipBytes[1] = ipBytes[2]
		ipBytes[2] = ipBytes[3]
	}

	ipBytes[3] = 1

	return fmt.Sprintf("%s/%d", ipBytes.String(), overlaySize), dev, ipStr, err
}

func (n *bridge) addressForSubnet(subnet *net.IPNet) (net.IP, string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return net.IP{}, "", err
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}

			if subnet.Contains(ip) {
				return ip, iface.Name, nil
			}
		}
	}

	return net.IP{}, "", fmt.Errorf("No address found in subnet")
}

func (n *bridge) killForkDNS() error {
	// Check if we have a running forkdns at all
	pidPath := shared.VarPath("networks", n.name, "forkdns.pid")

	// If the pid file doesn't exist, there is no process to kill.
	if !shared.PathExists(pidPath) {
		return nil
	}

	p, err := subprocess.ImportProcess(pidPath)
	if err != nil {
		return fmt.Errorf("Could not read pid file: %s", err)
	}

	err = p.Stop()
	if err != nil && err != subprocess.ErrNotRunning {
		return fmt.Errorf("Unable to kill dnsmasq: %s", err)
	}

	return nil
}

// updateForkdnsServersFile takes a list of node addresses and writes them atomically to
// the forkdns.servers file ready for forkdns to notice and re-apply its config.
func (n *bridge) updateForkdnsServersFile(addresses []string) error {
	// We don't want to race with ourselves here
	forkdnsServersLock.Lock()
	defer forkdnsServersLock.Unlock()

	permName := shared.VarPath("networks", n.name, ForkdnsServersListPath+"/"+ForkdnsServersListFile)
	tmpName := permName + ".tmp"

	// Open tmp file and truncate
	tmpFile, err := os.Create(tmpName)
	if err != nil {
		return err
	}
	defer tmpFile.Close()

	for _, address := range addresses {
		_, err := tmpFile.WriteString(address + "\n")
		if err != nil {
			return err
		}
	}

	tmpFile.Close()

	// Atomically rename finished file into permanent location so forkdns can pick it up.
	err = os.Rename(tmpName, permName)
	if err != nil {
		return err
	}

	return nil
}

// HasIPv4Firewall indicates whether the network has IPv4 firewall enabled.
func (n *bridge) HasIPv4Firewall() bool {
	if n.config["ipv4.firewall"] == "" || shared.IsTrue(n.config["ipv4.firewall"]) {
		return true
	}

	return false
}

// HasIPv6Firewall indicates whether the network has IPv6 firewall enabled.
func (n *bridge) HasIPv6Firewall() bool {
	if n.config["ipv6.firewall"] == "" || shared.IsTrue(n.config["ipv6.firewall"]) {
		return true
	}

	return false
}
