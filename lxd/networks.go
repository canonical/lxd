package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	log "github.com/lxc/lxd/shared/log15"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

// API endpoints
func networksGet(d *Daemon, r *http.Request) Response {
	recursionStr := r.FormValue("recursion")
	recursion, err := strconv.Atoi(recursionStr)
	if err != nil {
		recursion = 0
	}

	ifs, err := networkGetInterfaces(d.db)
	if err != nil {
		return InternalError(err)
	}

	resultString := []string{}
	resultMap := []api.Network{}
	for _, iface := range ifs {
		if recursion == 0 {
			resultString = append(resultString, fmt.Sprintf("/%s/networks/%s", version.APIVersion, iface))
		} else {
			net, err := doNetworkGet(d, iface)
			if err != nil {
				continue
			}
			resultMap = append(resultMap, net)
		}
	}

	if recursion == 0 {
		return SyncResponse(true, resultString)
	}

	return SyncResponse(true, resultMap)
}

func networksPost(d *Daemon, r *http.Request) Response {
	req := api.NetworksPost{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	err = networkValidName(req.Name)
	if err != nil {
		return BadRequest(err)
	}

	if req.Type != "" && req.Type != "bridge" {
		return BadRequest(fmt.Errorf("Only 'bridge' type networks can be created"))
	}

	networks, err := networkGetInterfaces(d.db)
	if err != nil {
		return InternalError(err)
	}

	if shared.StringInSlice(req.Name, networks) {
		return BadRequest(fmt.Errorf("The network already exists"))
	}

	if req.Config == nil {
		req.Config = map[string]string{}
	}

	err = networkValidateConfig(req.Name, req.Config)
	if err != nil {
		return BadRequest(err)
	}

	// Set some default values where needed
	if req.Config["bridge.mode"] == "fan" {
		if req.Config["fan.underlay_subnet"] == "" {
			req.Config["fan.underlay_subnet"] = "auto"
		}
	} else {
		if req.Config["ipv4.address"] == "" {
			req.Config["ipv4.address"] = "auto"
		}
		if req.Config["ipv4.address"] == "auto" && req.Config["ipv4.nat"] == "" {
			req.Config["ipv4.nat"] = "true"
		}

		if req.Config["ipv6.address"] == "" {
			content, err := ioutil.ReadFile("/proc/sys/net/ipv6/conf/default/disable_ipv6")
			if err == nil && string(content) == "0\n" {
				req.Config["ipv6.address"] = "auto"
			}
		}
		if req.Config["ipv6.address"] == "auto" && req.Config["ipv6.nat"] == "" {
			req.Config["ipv6.nat"] = "true"
		}
	}

	// Replace "auto" by actual values
	err = networkFillAuto(req.Config)
	if err != nil {
		return InternalError(err)
	}

	// Create the database entry
	_, err = d.db.NetworkCreate(req.Name, req.Description, req.Config)
	if err != nil {
		return InternalError(
			fmt.Errorf("Error inserting %s into database: %s", req.Name, err))
	}

	// Start the network
	n, err := networkLoadByName(d.State(), req.Name)
	if err != nil {
		return InternalError(err)
	}

	err = n.Start()
	if err != nil {
		n.Delete()
		return InternalError(err)
	}

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/networks/%s", version.APIVersion, req.Name))
}

var networksCmd = Command{name: "networks", get: networksGet, post: networksPost}

func networkGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	n, err := doNetworkGet(d, name)
	if err != nil {
		return SmartError(err)
	}

	etag := []interface{}{n.Name, n.Managed, n.Type, n.Description, n.Config}

	return SyncResponseETag(true, &n, etag)
}

func doNetworkGet(d *Daemon, name string) (api.Network, error) {
	// Get some information
	osInfo, _ := net.InterfaceByName(name)
	_, dbInfo, _ := d.db.NetworkGet(name)

	// Sanity check
	if osInfo == nil && dbInfo == nil {
		return api.Network{}, os.ErrNotExist
	}

	// Prepare the response
	n := api.Network{}
	n.Name = name
	n.UsedBy = []string{}
	n.Config = map[string]string{}

	// Look for containers using the interface
	cts, err := d.db.ContainersList(db.CTypeRegular)
	if err != nil {
		return api.Network{}, err
	}

	for _, ct := range cts {
		c, err := containerLoadByName(d.State(), ct)
		if err != nil {
			return api.Network{}, err
		}

		if networkIsInUse(c, n.Name) {
			n.UsedBy = append(n.UsedBy, fmt.Sprintf("/%s/containers/%s", version.APIVersion, ct))
		}
	}

	// Set the device type as needed
	if osInfo != nil && shared.IsLoopback(osInfo) {
		n.Type = "loopback"
	} else if dbInfo != nil || shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", n.Name)) {
		if dbInfo != nil {
			n.Managed = true
			n.Description = dbInfo.Description
			n.Config = dbInfo.Config
		}

		n.Type = "bridge"
	} else if shared.PathExists(fmt.Sprintf("/proc/net/vlan/%s", n.Name)) {
		n.Type = "vlan"
	} else if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/device", n.Name)) {
		n.Type = "physical"
	} else if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bonding", n.Name)) {
		n.Type = "bond"
	} else {
		_, err := shared.RunCommand("ovs-vsctl", "br-exists", n.Name)
		if err == nil {
			n.Type = "bridge"
		} else {
			n.Type = "unknown"
		}
	}

	return n, nil
}

func networkDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	state := d.State()

	// Get the existing network
	n, err := networkLoadByName(state, name)
	if err != nil {
		return NotFound
	}

	// Attempt to delete the network
	err = n.Delete()
	if err != nil {
		return SmartError(err)
	}

	// Cleanup storage
	if shared.PathExists(shared.VarPath("networks", n.name)) {
		os.RemoveAll(shared.VarPath("networks", n.name))
	}

	return EmptySyncResponse
}

func networkPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	req := api.NetworkPost{}
	state := d.State()

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Get the existing network
	n, err := networkLoadByName(state, name)
	if err != nil {
		return NotFound
	}

	// Sanity checks
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	err = networkValidName(req.Name)
	if err != nil {
		return BadRequest(err)
	}

	// Check that the name isn't already in use
	networks, err := networkGetInterfaces(d.db)
	if err != nil {
		return InternalError(err)
	}

	if shared.StringInSlice(req.Name, networks) {
		return Conflict
	}

	// Rename it
	err = n.Rename(req.Name)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/networks/%s", version.APIVersion, req.Name))
}

func networkPut(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	// Get the existing network
	_, dbInfo, err := d.db.NetworkGet(name)
	if err != nil {
		return SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{dbInfo.Name, dbInfo.Managed, dbInfo.Type, dbInfo.Description, dbInfo.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.NetworkPut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	return doNetworkUpdate(d, name, dbInfo.Config, req)
}

func networkPatch(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	// Get the existing network
	_, dbInfo, err := d.db.NetworkGet(name)
	if dbInfo != nil {
		return SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{dbInfo.Name, dbInfo.Managed, dbInfo.Type, dbInfo.Description, dbInfo.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	req := api.NetworkPut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Config stacking
	if req.Config == nil {
		req.Config = map[string]string{}
	}

	for k, v := range dbInfo.Config {
		_, ok := req.Config[k]
		if !ok {
			req.Config[k] = v
		}
	}

	return doNetworkUpdate(d, name, dbInfo.Config, req)
}

func doNetworkUpdate(d *Daemon, name string, oldConfig map[string]string, req api.NetworkPut) Response {
	// Validate the configuration
	err := networkValidateConfig(name, req.Config)
	if err != nil {
		return BadRequest(err)
	}

	// When switching to a fan bridge, auto-detect the underlay
	if req.Config["bridge.mode"] == "fan" {
		if req.Config["fan.underlay_subnet"] == "" {
			req.Config["fan.underlay_subnet"] = "auto"
		}
	}

	// Load the network
	n, err := networkLoadByName(d.State(), name)
	if err != nil {
		return NotFound
	}

	err = n.Update(req)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

var networkCmd = Command{name: "networks/{name}", get: networkGet, delete: networkDelete, post: networkPost, put: networkPut, patch: networkPatch}

// The network structs and functions
func networkLoadByName(s *state.State, name string) (*network, error) {
	id, dbInfo, err := s.DB.NetworkGet(name)
	if err != nil {
		return nil, err
	}

	n := network{db: s.DB, state: s, id: id, name: name, description: dbInfo.Description, config: dbInfo.Config}

	return &n, nil
}

func networkStartup(s *state.State) error {
	// Get a list of managed networks
	networks, err := s.DB.Networks()
	if err != nil {
		return err
	}

	// Bring them all up
	for _, name := range networks {
		n, err := networkLoadByName(s, name)
		if err != nil {
			return err
		}

		err = n.Start()
		if err != nil {
			// Don't cause LXD to fail to start entirely on network bring up failure
			logger.Error("Failed to bring up network", log.Ctx{"err": err, "name": name})
		}
	}

	return nil
}

func networkShutdown(s *state.State) error {
	// Get a list of managed networks
	networks, err := s.DB.Networks()
	if err != nil {
		return err
	}

	// Bring them all up
	for _, name := range networks {
		n, err := networkLoadByName(s, name)
		if err != nil {
			return err
		}

		if !n.IsRunning() {
			continue
		}

		err = n.Stop()
		if err != nil {
			logger.Error("Failed to bring down network", log.Ctx{"err": err, "name": name})
		}
	}

	return nil
}

type network struct {
	// Properties
	db          *db.Node
	state       *state.State
	id          int64
	name        string
	description string

	// config
	config map[string]string
}

func (n *network) Config() map[string]string {
	return n.config
}

func (n *network) IsRunning() bool {
	return shared.PathExists(fmt.Sprintf("/sys/class/net/%s", n.name))
}

func (n *network) IsUsed() bool {
	// Look for containers using the interface
	cts, err := n.db.ContainersList(db.CTypeRegular)
	if err != nil {
		return true
	}

	for _, ct := range cts {
		c, err := containerLoadByName(n.state, ct)
		if err != nil {
			return true
		}

		if networkIsInUse(c, n.name) {
			return true
		}
	}

	return false
}

func (n *network) Delete() error {
	// Sanity checks
	if n.IsUsed() {
		return fmt.Errorf("The network is currently in use")
	}

	// Bring the network down
	if n.IsRunning() {
		err := n.Stop()
		if err != nil {
			return err
		}
	}

	// Remove the network from the database
	err := n.db.NetworkDelete(n.name)
	if err != nil {
		return err
	}

	return nil
}

func (n *network) Rename(name string) error {
	// Sanity checks
	if n.IsUsed() {
		return fmt.Errorf("The network is currently in use")
	}

	// Bring the network down
	if n.IsRunning() {
		err := n.Stop()
		if err != nil {
			return err
		}
	}

	// Rename directory
	if shared.PathExists(shared.VarPath("networks", name)) {
		os.RemoveAll(shared.VarPath("networks", name))
	}

	if shared.PathExists(shared.VarPath("networks", n.name)) {
		err := os.Rename(shared.VarPath("networks", n.name), shared.VarPath("networks", name))
		if err != nil {
			return err
		}
	}

	// Rename the database entry
	err := n.db.NetworkRename(n.name, name)
	if err != nil {
		return err
	}
	n.name = name

	// Bring the network up
	err = n.Start()
	if err != nil {
		return err
	}

	return nil
}

func (n *network) Start() error {
	// If we are in mock mode, just no-op.
	if n.state.OS.MockMode {
		return nil
	}

	// Create directory
	if !shared.PathExists(shared.VarPath("networks", n.name)) {
		err := os.MkdirAll(shared.VarPath("networks", n.name), 0711)
		if err != nil {
			return err
		}
	}

	// Create the bridge interface
	if !n.IsRunning() {
		if n.config["bridge.driver"] == "openvswitch" {
			_, err := shared.RunCommand("ovs-vsctl", "add-br", n.name)
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
	tunnels := networkGetTunnels(n.config)

	// IPv6 bridge configuration
	if !shared.StringInSlice(n.config["ipv6.address"], []string{"", "none"}) {
		err := networkSysctl(fmt.Sprintf("ipv6/conf/%s/autoconf", n.name), "0")
		if err != nil {
			return err
		}

		err = networkSysctl(fmt.Sprintf("ipv6/conf/%s/accept_dad", n.name), "0")
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
			networkAttachInterface(n.name, fmt.Sprintf("%s-mtu", n.name))
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

			err = networkAttachInterface(n.name, entry)
			if err != nil {
				return err
			}
		}
	}

	// Remove any existing IPv4 iptables rules
	err = networkIptablesClear("ipv4", n.name, "")
	if err != nil {
		return err
	}

	err = networkIptablesClear("ipv4", n.name, "mangle")
	if err != nil {
		return err
	}

	err = networkIptablesClear("ipv4", n.name, "nat")
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
		if n.config["ipv4.dhcp"] == "" || shared.IsTrue(n.config["ipv4.dhcp"]) {
			// Setup basic iptables overrides for DHCP/DNS
			rules := [][]string{
				{"ipv4", n.name, "", "INPUT", "-i", n.name, "-p", "udp", "--dport", "67", "-j", "ACCEPT"},
				{"ipv4", n.name, "", "INPUT", "-i", n.name, "-p", "udp", "--dport", "53", "-j", "ACCEPT"},
				{"ipv4", n.name, "", "INPUT", "-i", n.name, "-p", "tcp", "--dport", "53", "-j", "ACCEPT"},
				{"ipv4", n.name, "", "OUTPUT", "-o", n.name, "-p", "udp", "--sport", "67", "-j", "ACCEPT"},
				{"ipv4", n.name, "", "OUTPUT", "-o", n.name, "-p", "udp", "--sport", "53", "-j", "ACCEPT"},
				{"ipv4", n.name, "", "OUTPUT", "-o", n.name, "-p", "tcp", "--sport", "53", "-j", "ACCEPT"}}

			for _, rule := range rules {
				err = networkIptablesPrepend(rule[0], rule[1], rule[2], rule[3], rule[4:]...)
				if err != nil {
					return err
				}
			}
		}

		// Attempt a workaround for broken DHCP clients
		networkIptablesPrepend("ipv4", n.name, "mangle", "POSTROUTING", "-o", n.name, "-p", "udp", "--dport", "68", "-j", "CHECKSUM", "--checksum-fill")

		// Allow forwarding
		if n.config["bridge.mode"] == "fan" || n.config["ipv4.routing"] == "" || shared.IsTrue(n.config["ipv4.routing"]) {
			err = networkSysctl("ipv4/ip_forward", "1")
			if err != nil {
				return err
			}

			if n.config["ipv4.firewall"] == "" || shared.IsTrue(n.config["ipv4.firewall"]) {
				err = networkIptablesPrepend("ipv4", n.name, "", "FORWARD", "-i", n.name, "-j", "ACCEPT")
				if err != nil {
					return err
				}

				err = networkIptablesPrepend("ipv4", n.name, "", "FORWARD", "-o", n.name, "-j", "ACCEPT")
				if err != nil {
					return err
				}
			}
		} else {
			if n.config["ipv4.firewall"] == "" || shared.IsTrue(n.config["ipv4.firewall"]) {
				err = networkIptablesPrepend("ipv4", n.name, "", "FORWARD", "-i", n.name, "-j", "REJECT")
				if err != nil {
					return err
				}

				err = networkIptablesPrepend("ipv4", n.name, "", "FORWARD", "-o", n.name, "-j", "REJECT")
				if err != nil {
					return err
				}
			}
		}
	}

	// Start building the dnsmasq command line
	dnsmasqCmd := []string{"dnsmasq", "--strict-order", "--bind-interfaces",
		fmt.Sprintf("--pid-file=%s", shared.VarPath("networks", n.name, "dnsmasq.pid")),
		"--except-interface=lo",
		fmt.Sprintf("--interface=%s", n.name)}

	if !debug {
		// --quiet options are only supported on >2.67
		minVer, _ := version.NewDottedVersion("2.67")

		v, err := networkGetDnsmasqVersion()
		if err == nil && v.Compare(minVer) > 0 {
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
		if n.config["ipv4.dhcp"] == "" || shared.IsTrue(n.config["ipv4.dhcp"]) {
			if !shared.StringInSlice("--dhcp-no-override", dnsmasqCmd) {
				dnsmasqCmd = append(dnsmasqCmd, []string{"--dhcp-no-override", "--dhcp-authoritative", fmt.Sprintf("--dhcp-leasefile=%s", shared.VarPath("networks", n.name, "dnsmasq.leases")), fmt.Sprintf("--dhcp-hostsfile=%s", shared.VarPath("networks", n.name, "dnsmasq.hosts"))}...)
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
				dnsmasqCmd = append(dnsmasqCmd, []string{"--dhcp-range", fmt.Sprintf("%s,%s,%s", networkGetIP(subnet, 2).String(), networkGetIP(subnet, -2).String(), expiry)}...)
			}
		}

		// Add the address
		_, err = shared.RunCommand("ip", "-4", "addr", "add", "dev", n.name, n.config["ipv4.address"])
		if err != nil {
			return err
		}

		// Configure NAT
		if shared.IsTrue(n.config["ipv4.nat"]) {
			err = networkIptablesPrepend("ipv4", n.name, "nat", "POSTROUTING", "-s", subnet.String(), "!", "-d", subnet.String(), "-j", "MASQUERADE")
			if err != nil {
				return err
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
	}

	// Remove any existing IPv6 iptables rules
	err = networkIptablesClear("ipv6", n.name, "")
	if err != nil {
		return err
	}

	err = networkIptablesClear("ipv6", n.name, "nat")
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
		err := networkSysctl(fmt.Sprintf("ipv6/conf/%s/disable_ipv6", n.name), "0")
		if err != nil {
			return err
		}

		// Parse the subnet
		ip, subnet, err := net.ParseCIDR(n.config["ipv6.address"])
		if err != nil {
			return err
		}

		// Update the dnsmasq config
		dnsmasqCmd = append(dnsmasqCmd, []string{fmt.Sprintf("--listen-address=%s", ip.String()), "--enable-ra"}...)
		if n.config["ipv6.dhcp"] == "" || shared.IsTrue(n.config["ipv6.dhcp"]) {
			// Setup basic iptables overrides for DHCP/DNS
			rules := [][]string{
				{"ipv6", n.name, "", "INPUT", "-i", n.name, "-p", "udp", "--dport", "546", "-j", "ACCEPT"},
				{"ipv6", n.name, "", "INPUT", "-i", n.name, "-p", "udp", "--dport", "53", "-j", "ACCEPT"},
				{"ipv6", n.name, "", "INPUT", "-i", n.name, "-p", "tcp", "--dport", "53", "-j", "ACCEPT"},
				{"ipv6", n.name, "", "OUTPUT", "-o", n.name, "-p", "udp", "--sport", "546", "-j", "ACCEPT"},
				{"ipv6", n.name, "", "OUTPUT", "-o", n.name, "-p", "udp", "--sport", "53", "-j", "ACCEPT"},
				{"ipv6", n.name, "", "OUTPUT", "-o", n.name, "-p", "tcp", "--sport", "53", "-j", "ACCEPT"}}

			for _, rule := range rules {
				err = networkIptablesPrepend(rule[0], rule[1], rule[2], rule[3], rule[4:]...)
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
						dnsmasqCmd = append(dnsmasqCmd, []string{"--dhcp-range", fmt.Sprintf("%s,%s", strings.Replace(dhcpRange, "-", ",", -1), expiry)}...)
					}
				} else {
					dnsmasqCmd = append(dnsmasqCmd, []string{"--dhcp-range", fmt.Sprintf("%s,%s,%s", networkGetIP(subnet, 2), networkGetIP(subnet, -1), expiry)}...)
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

				err = networkSysctl(fmt.Sprintf("ipv6/conf/%s/accept_ra", entry.Name()), "2")
				if err != nil && !os.IsNotExist(err) {
					return err
				}
			}

			// Then set forwarding for all of them
			for _, entry := range entries {
				err = networkSysctl(fmt.Sprintf("ipv6/conf/%s/forwarding", entry.Name()), "1")
				if err != nil && !os.IsNotExist(err) {
					return err
				}
			}

			if n.config["ipv6.firewall"] == "" || shared.IsTrue(n.config["ipv6.firewall"]) {
				err = networkIptablesPrepend("ipv6", n.name, "", "FORWARD", "-i", n.name, "-j", "ACCEPT")
				if err != nil {
					return err
				}

				err = networkIptablesPrepend("ipv6", n.name, "", "FORWARD", "-o", n.name, "-j", "ACCEPT")
				if err != nil {
					return err
				}
			}
		} else {
			if n.config["ipv6.firewall"] == "" || shared.IsTrue(n.config["ipv6.firewall"]) {
				err = networkIptablesPrepend("ipv6", n.name, "", "FORWARD", "-i", n.name, "-j", "REJECT")
				if err != nil {
					return err
				}

				err = networkIptablesPrepend("ipv6", n.name, "", "FORWARD", "-o", n.name, "-j", "REJECT")
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
			err = networkIptablesPrepend("ipv6", n.name, "nat", "POSTROUTING", "-s", subnet.String(), "!", "-d", subnet.String(), "-j", "MASQUERADE")
			if err != nil {
				return err
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
	}

	// Configure the fan
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

		_, overlaySubnet, err := net.ParseCIDR(overlay)
		if err != nil {
			return err
		}

		// Get the address
		fanAddress, devName, devAddr, err := networkFanAddress(underlaySubnet, overlaySubnet)
		if err != nil {
			return err
		}

		addr := strings.Split(fanAddress, "/")
		if n.config["fan.type"] == "ipip" {
			fanAddress = fmt.Sprintf("%s/24", addr[0])
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
		dnsmasqCmd = append(dnsmasqCmd, []string{
			fmt.Sprintf("--listen-address=%s", addr[0]),
			"--dhcp-no-override", "--dhcp-authoritative",
			fmt.Sprintf("--dhcp-leasefile=%s", shared.VarPath("networks", n.name, "dnsmasq.leases")),
			fmt.Sprintf("--dhcp-hostsfile=%s", shared.VarPath("networks", n.name, "dnsmasq.hosts")),
			"--dhcp-range", fmt.Sprintf("%s,%s", networkGetIP(hostSubnet, 2).String(), networkGetIP(hostSubnet, -2).String())}...)

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

			err = networkAttachInterface(n.name, tunName)
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
		err = networkIptablesPrepend("ipv4", n.name, "nat", "POSTROUTING", "-s", underlaySubnet.String(), "!", "-d", underlaySubnet.String(), "-j", "MASQUERADE")
		if err != nil {
			return err
		}
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
					_, devName, err = networkDefaultGatewaySubnetV4()
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

			tunId := getConfig("id")
			if tunId == "" {
				tunId = "1"
			}
			cmd = append(cmd, []string{"id", tunId}...)
		}

		// Create the interface
		_, err = shared.RunCommand(cmd[0], cmd[1:]...)
		if err != nil {
			return err
		}

		// Bridge it and bring up
		err = networkAttachInterface(n.name, tunName)
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

	// Kill any existing dnsmasq daemon for this network
	err = networkKillDnsmasq(n.name, false)
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
			dnsmasqCmd = append(dnsmasqCmd, []string{"-s", dnsDomain, "-S", fmt.Sprintf("/%s/", dnsDomain)}...)
		}

		// Create a config file to contain additional config (and to prevent dnsmasq from reading /etc/dnsmasq.conf)
		err = ioutil.WriteFile(shared.VarPath("networks", n.name, "dnsmasq.raw"), []byte(fmt.Sprintf("%s\n", n.config["raw.dnsmasq"])), 0644)
		if err != nil {
			return err
		}
		dnsmasqCmd = append(dnsmasqCmd, fmt.Sprintf("--conf-file=%s", shared.VarPath("networks", n.name, "dnsmasq.raw")))

		// Attempt to drop privileges
		for _, user := range []string{"lxd", "nobody"} {
			_, err := shared.UserId(user)
			if err != nil {
				continue
			}

			dnsmasqCmd = append(dnsmasqCmd, []string{"-u", user}...)
			break
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
			return fmt.Errorf("dnsmasq is required for LXD managed bridges.")
		}

		// Start dnsmasq (occasionally races, try a few times)
		output, err := shared.TryRunCommand(dnsmasqCmd[0], dnsmasqCmd[1:]...)
		if err != nil {
			return fmt.Errorf("Failed to run: %s: %s", strings.Join(dnsmasqCmd, " "), strings.TrimSpace(output))
		}

		// Update the static leases
		err = networkUpdateStatic(n.state, n.name)
		if err != nil {
			return err
		}
	}

	return nil
}

func (n *network) Stop() error {
	if !n.IsRunning() {
		return fmt.Errorf("The network is already stopped")
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

	// Cleanup iptables
	err := networkIptablesClear("ipv4", n.name, "")
	if err != nil {
		return err
	}

	err = networkIptablesClear("ipv4", n.name, "mangle")
	if err != nil {
		return err
	}

	err = networkIptablesClear("ipv4", n.name, "nat")
	if err != nil {
		return err
	}

	err = networkIptablesClear("ipv6", n.name, "")
	if err != nil {
		return err
	}

	err = networkIptablesClear("ipv6", n.name, "nat")
	if err != nil {
		return err
	}

	// Kill any existing dnsmasq daemon for this network
	err = networkKillDnsmasq(n.name, false)
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

func (n *network) Update(newNetwork api.NetworkPut) error {
	err := networkFillAuto(newNetwork.Config)
	if err != nil {
		return err
	}
	newConfig := newNetwork.Config

	// Backup the current state
	oldConfig := map[string]string{}
	oldDescription := n.description
	err = shared.DeepCopy(&n.config, &oldConfig)
	if err != nil {
		return err
	}

	// Define a function which reverts everything.  Defer this function
	// so that it doesn't need to be explicitly called in every failing
	// return path.  Track whether or not we want to undo the changes
	// using a closure.
	undoChanges := true
	defer func() {
		if undoChanges {
			n.config = oldConfig
			n.description = oldDescription
		}
	}()

	// Diff the configurations
	changedConfig := []string{}
	userOnly := true
	for key := range oldConfig {
		if oldConfig[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	for key := range newConfig {
		if oldConfig[key] != newConfig[key] {
			if !strings.HasPrefix(key, "user.") {
				userOnly = false
			}

			if !shared.StringInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	// Skip on no change
	if len(changedConfig) == 0 && newNetwork.Description == n.description {
		return nil
	}

	// Update the network
	if !userOnly {
		if shared.StringInSlice("bridge.driver", changedConfig) && n.IsRunning() {
			err = n.Stop()
			if err != nil {
				return err
			}
		}

		if shared.StringInSlice("bridge.external_interfaces", changedConfig) && n.IsRunning() {
			devices := []string{}
			for _, dev := range strings.Split(newConfig["bridge.external_interfaces"], ",") {
				dev = strings.TrimSpace(dev)
				devices = append(devices, dev)
			}

			for _, dev := range strings.Split(oldConfig["bridge.external_interfaces"], ",") {
				dev = strings.TrimSpace(dev)
				if dev == "" {
					continue
				}

				if !shared.StringInSlice(dev, devices) && shared.PathExists(fmt.Sprintf("/sys/class/net/%s", dev)) {
					err = networkDetachInterface(n.name, dev)
					if err != nil {
						return err
					}
				}
			}
		}
	}

	// Apply changes
	n.config = newConfig
	n.description = newNetwork.Description

	// Update the database
	err = n.db.NetworkUpdate(n.name, n.description, n.config)
	if err != nil {
		return err
	}

	// Restart the network
	if !userOnly {
		err = n.Start()
		if err != nil {
			return err
		}
	}

	// Success, update the closure to mark that the changes should be kept.
	undoChanges = false

	return nil
}
