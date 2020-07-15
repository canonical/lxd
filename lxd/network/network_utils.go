package network

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"math"
	"math/big"
	"math/rand"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/dnsmasq"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

// ValidNetworkName validates network name.
func ValidNetworkName(value string) error {
	// Not a veth-liked name
	if strings.HasPrefix(value, "veth") {
		return fmt.Errorf("Interface name cannot be prefix with veth")
	}

	// Validate the length
	if len(value) < 2 {
		return fmt.Errorf("Interface name is too short (minimum 2 characters)")
	}

	if len(value) > 15 {
		return fmt.Errorf("Interface name is too long (maximum 15 characters)")
	}

	// Validate the character set
	match, _ := regexp.MatchString("^[-_a-zA-Z0-9.]*$", value)
	if !match {
		return fmt.Errorf("Interface name contains invalid characters")
	}

	return nil
}

func networkValidPort(value string) error {
	if value == "" {
		return nil
	}

	valueInt, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fmt.Errorf("Invalid value for an integer: %s", value)
	}

	if valueInt < 1 || valueInt > 65536 {
		return fmt.Errorf("Invalid port number: %s", value)
	}

	return nil
}

// IsInUseByInstance indicates if network is referenced by an instance's NIC devices.
// Checks if the device's parent or network properties match the network name.
func IsInUseByInstance(c instance.Instance, networkName string) bool {
	return isInUseByDevices(c.ExpandedDevices(), networkName)
}

// IsInUseByProfile indicates if network is referenced by a profile's NIC devices.
// Checks if the device's parent or network properties match the network name.
func IsInUseByProfile(profile api.Profile, networkName string) bool {
	return isInUseByDevices(deviceConfig.NewDevices(profile.Devices), networkName)
}

func isInUseByDevices(devices deviceConfig.Devices, networkName string) bool {
	for _, d := range devices {
		if d["type"] != "nic" {
			continue
		}

		if !shared.StringInSlice(d.NICType(), []string{"bridged", "macvlan", "ipvlan", "physical", "sriov"}) {
			continue
		}

		// Temporarily populate parent from network setting if used.
		if d["network"] != "" {
			d["parent"] = d["network"]
		}

		if d["parent"] == "" {
			continue
		}

		if GetHostDevice(d["parent"], d["vlan"]) == networkName {
			return true
		}
	}

	return false
}

// GetIP returns a net.IP representing the IP belonging to the subnet for the host number supplied.
func GetIP(subnet *net.IPNet, host int64) net.IP {
	// Convert IP to a big int.
	bigIP := big.NewInt(0)
	bigIP.SetBytes(subnet.IP.To16())

	// Deal with negative offsets.
	bigHost := big.NewInt(host)
	bigCount := big.NewInt(host)
	if host < 0 {
		mask, size := subnet.Mask.Size()

		bigHosts := big.NewFloat(0)
		bigHosts.SetFloat64((math.Pow(2, float64(size-mask))))
		bigHostsInt, _ := bigHosts.Int(nil)

		bigCount.Set(bigHostsInt)
		bigCount.Add(bigCount, bigHost)
	}

	// Get the new IP int.
	bigIP.Add(bigIP, bigCount)

	// Generate an IPv6.
	if subnet.IP.To4() == nil {
		newIP := bigIP.Bytes()
		return newIP
	}

	// Generate an IPv4.
	newIP := make(net.IP, 4)
	binary.BigEndian.PutUint32(newIP, uint32(bigIP.Int64()))
	return newIP
}

// IsNativeBridge returns whether the bridge name specified is a Linux native bridge.
func IsNativeBridge(bridgeName string) bool {
	return shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", bridgeName))
}

// AttachInterface attaches an interface to a bridge.
func AttachInterface(bridgeName string, devName string) error {
	if IsNativeBridge(bridgeName) {
		_, err := shared.RunCommand("ip", "link", "set", "dev", devName, "master", bridgeName)
		if err != nil {
			return err
		}
	} else {
		// Check if interface is already connected to a bridge, if not connect it to the specified bridge.
		_, err := shared.RunCommand("ovs-vsctl", "port-to-br", devName)
		if err != nil {
			_, err := shared.RunCommand("ovs-vsctl", "add-port", bridgeName, devName)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// DetachInterface detaches an interface from a bridge.
func DetachInterface(bridgeName string, devName string) error {
	if IsNativeBridge(bridgeName) {
		_, err := shared.RunCommand("ip", "link", "set", "dev", devName, "nomaster")
		if err != nil {
			return err
		}
	} else {
		// Check if interface is connected to a bridge, if so, then remove it from the bridge.
		_, err := shared.RunCommand("ovs-vsctl", "port-to-br", devName)
		if err == nil {
			_, err := shared.RunCommand("ovs-vsctl", "del-port", bridgeName, devName)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// GetDevMTU retrieves the current MTU setting for a named network device.
func GetDevMTU(devName string) (uint64, error) {
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

// DefaultGatewaySubnetV4 returns subnet of default gateway interface.
func DefaultGatewaySubnetV4() (*net.IPNet, string, error) {
	file, err := os.Open("/proc/net/route")
	if err != nil {
		return nil, "", err
	}
	defer file.Close()

	ifaceName := ""

	scanner := bufio.NewReader(file)
	for {
		line, _, err := scanner.ReadLine()
		if err != nil {
			break
		}

		fields := strings.Fields(string(line))

		if fields[1] == "00000000" && fields[7] == "00000000" {
			ifaceName = fields[0]
			break
		}
	}

	if ifaceName == "" {
		return nil, "", fmt.Errorf("No default gateway for IPv4")
	}

	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return nil, "", err
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return nil, "", err
	}

	var subnet *net.IPNet

	for _, addr := range addrs {
		addrIP, addrNet, err := net.ParseCIDR(addr.String())
		if err != nil {
			return nil, "", err
		}

		if addrIP.To4() == nil {
			continue
		}

		if subnet != nil {
			return nil, "", fmt.Errorf("More than one IPv4 subnet on default interface")
		}

		subnet = addrNet
	}

	if subnet == nil {
		return nil, "", fmt.Errorf("No IPv4 subnet on default interface")
	}

	return subnet, ifaceName, nil
}

// UpdateDNSMasqStatic rebuilds the DNSMasq static allocations.
func UpdateDNSMasqStatic(s *state.State, networkName string) error {
	// We don't want to race with ourselves here.
	dnsmasq.ConfigMutex.Lock()
	defer dnsmasq.ConfigMutex.Unlock()

	// Get all the networks.
	var networks []string
	if networkName == "" {
		var err error
		networks, err = s.Cluster.GetNetworks()
		if err != nil {
			return err
		}
	} else {
		networks = []string{networkName}
	}

	// Get all the instances.
	insts, err := instance.LoadNodeAll(s, instancetype.Any)
	if err != nil {
		return err
	}

	// Build a list of dhcp host entries.
	entries := map[string][][]string{}
	for _, inst := range insts {
		// Go through all its devices (including profiles).
		for k, d := range inst.ExpandedDevices() {
			// Skip uninteresting entries.
			if d["type"] != "nic" || d.NICType() != "bridged" {
				continue
			}

			// Temporarily populate parent from network setting if used.
			if d["network"] != "" {
				d["parent"] = d["network"]
			}

			// Skip devices not connected to managed networks.
			if !shared.StringInSlice(d["parent"], networks) {
				continue
			}

			// Fill in the hwaddr from volatile.
			d, err = inst.FillNetworkDevice(k, d)
			if err != nil {
				continue
			}

			// Add the new host entries.
			_, ok := entries[d["parent"]]
			if !ok {
				entries[d["parent"]] = [][]string{}
			}

			if (shared.IsTrue(d["security.ipv4_filtering"]) && d["ipv4.address"] == "") || (shared.IsTrue(d["security.ipv6_filtering"]) && d["ipv6.address"] == "") {
				curIPv4, curIPv6, err := dnsmasq.DHCPStaticIPs(d["parent"], inst.Project(), inst.Name())
				if err != nil && !os.IsNotExist(err) {
					return err
				}

				if d["ipv4.address"] == "" && curIPv4.IP != nil {
					d["ipv4.address"] = curIPv4.IP.String()
				}

				if d["ipv6.address"] == "" && curIPv6.IP != nil {
					d["ipv6.address"] = curIPv6.IP.String()
				}
			}

			entries[d["parent"]] = append(entries[d["parent"]], []string{d["hwaddr"], inst.Project(), inst.Name(), d["ipv4.address"], d["ipv6.address"]})
		}
	}

	// Update the host files.
	for _, network := range networks {
		entries, _ := entries[network]

		// Skip networks we don't manage (or don't have DHCP enabled).
		if !shared.PathExists(shared.VarPath("networks", network, "dnsmasq.pid")) {
			continue
		}

		n, err := LoadByName(s, network)
		if err != nil {
			return err
		}
		config := n.Config()

		// Wipe everything clean.
		files, err := ioutil.ReadDir(shared.VarPath("networks", network, "dnsmasq.hosts"))
		if err != nil {
			return err
		}

		for _, entry := range files {
			err = os.Remove(shared.VarPath("networks", network, "dnsmasq.hosts", entry.Name()))
			if err != nil {
				return err
			}
		}

		// Apply the changes.
		for entryIdx, entry := range entries {
			hwaddr := entry[0]
			projectName := entry[1]
			cName := entry[2]
			ipv4Address := entry[3]
			ipv6Address := entry[4]
			line := hwaddr

			// Look for duplicates.
			duplicate := false
			for iIdx, i := range entries {
				if project.Instance(entry[1], entry[2]) == project.Instance(i[1], i[2]) {
					// Skip ourselves.
					continue
				}

				if entry[0] == i[0] {
					// Find broken configurations
					logger.Errorf("Duplicate MAC detected: %s and %s", project.Instance(entry[1], entry[2]), project.Instance(i[1], i[2]))
				}

				if i[3] == "" && i[4] == "" {
					// Skip unconfigured.
					continue
				}

				if entry[3] == i[3] && entry[4] == i[4] {
					// Find identical containers (copies with static configuration).
					if entryIdx > iIdx {
						duplicate = true
					} else {
						line = fmt.Sprintf("%s,%s", line, i[0])
						logger.Debugf("Found containers with duplicate IPv4/IPv6: %s and %s", project.Instance(entry[1], entry[2]), project.Instance(i[1], i[2]))
					}
				}
			}

			if duplicate {
				continue
			}

			// Generate the dhcp-host line.
			err := dnsmasq.UpdateStaticEntry(network, projectName, cName, config, hwaddr, ipv4Address, ipv6Address)
			if err != nil {
				return err
			}
		}

		// Signal dnsmasq.
		err = dnsmasq.Kill(network, true)
		if err != nil {
			return err
		}
	}

	return nil
}

// ForkdnsServersList reads the server list file and returns the list as a slice.
func ForkdnsServersList(networkName string) ([]string, error) {
	servers := []string{}
	file, err := os.Open(shared.VarPath("networks", networkName, ForkdnsServersListPath, "/", ForkdnsServersListFile))
	if err != nil {
		return servers, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 0 {
			servers = append(servers, fields[0])
		}
	}
	if err := scanner.Err(); err != nil {
		return servers, err
	}

	return servers, nil
}

// FillConfig populates the supplied api.NetworkPost with automatically populated values.
func FillConfig(req *api.NetworksPost) error {
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
	err := fillAuto(req.Config)
	if err != nil {
		return err
	}
	return nil
}

func fillAuto(config map[string]string) error {
	if config["ipv4.address"] == "auto" {
		subnet, err := randomSubnetV4()
		if err != nil {
			return err
		}

		config["ipv4.address"] = subnet
	}

	if config["ipv6.address"] == "auto" {
		subnet, err := randomSubnetV6()
		if err != nil {
			return err
		}

		config["ipv6.address"] = subnet
	}

	if config["fan.underlay_subnet"] == "auto" {
		subnet, _, err := DefaultGatewaySubnetV4()
		if err != nil {
			return err
		}

		config["fan.underlay_subnet"] = subnet.String()
	}

	return nil
}

func randomSubnetV4() (string, error) {
	for i := 0; i < 100; i++ {
		cidr := fmt.Sprintf("10.%d.%d.1/24", rand.Intn(255), rand.Intn(255))
		_, subnet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}

		if inRoutingTable(subnet) {
			continue
		}

		if pingSubnet(subnet) {
			continue
		}

		return cidr, nil
	}

	return "", fmt.Errorf("Failed to automatically find an unused IPv4 subnet, manual configuration required")
}

func randomSubnetV6() (string, error) {
	for i := 0; i < 100; i++ {
		cidr := fmt.Sprintf("fd42:%x:%x:%x::1/64", rand.Intn(65535), rand.Intn(65535), rand.Intn(65535))
		_, subnet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}

		if inRoutingTable(subnet) {
			continue
		}

		if pingSubnet(subnet) {
			continue
		}

		return cidr, nil
	}

	return "", fmt.Errorf("Failed to automatically find an unused IPv6 subnet, manual configuration required")
}

func inRoutingTable(subnet *net.IPNet) bool {
	filename := "route"
	if subnet.IP.To4() == nil {
		filename = "ipv6_route"
	}

	file, err := os.Open(fmt.Sprintf("/proc/net/%s", filename))
	if err != nil {
		return false
	}
	defer file.Close()

	scanner := bufio.NewReader(file)
	for {
		line, _, err := scanner.ReadLine()
		if err != nil {
			break
		}

		fields := strings.Fields(string(line))

		// Get the IP
		var ip net.IP
		if filename == "ipv6_route" {
			ip, err = hex.DecodeString(fields[0])
			if err != nil {
				continue
			}
		} else {
			bytes, err := hex.DecodeString(fields[1])
			if err != nil {
				continue
			}

			ip = net.IPv4(bytes[3], bytes[2], bytes[1], bytes[0])
		}

		// Get the mask
		var mask net.IPMask
		if filename == "ipv6_route" {
			size, err := strconv.ParseInt(fmt.Sprintf("0x%s", fields[1]), 0, 64)
			if err != nil {
				continue
			}

			mask = net.CIDRMask(int(size), 128)
		} else {
			bytes, err := hex.DecodeString(fields[7])
			if err != nil {
				continue
			}

			mask = net.IPv4Mask(bytes[3], bytes[2], bytes[1], bytes[0])
		}

		// Generate a new network
		lineNet := net.IPNet{IP: ip, Mask: mask}

		// Ignore default gateway
		if lineNet.IP.Equal(net.ParseIP("::")) {
			continue
		}

		if lineNet.IP.Equal(net.ParseIP("0.0.0.0")) {
			continue
		}

		// Check if we have a route to our new subnet
		if lineNet.Contains(subnet.IP) {
			return true
		}
	}

	return false
}

func pingSubnet(subnet *net.IPNet) bool {
	var fail bool
	var failLock sync.Mutex
	var wgChecks sync.WaitGroup

	ping := func(ip net.IP) {
		defer wgChecks.Done()

		cmd := "ping"
		if ip.To4() == nil {
			cmd = "ping6"
		}

		_, err := shared.RunCommand(cmd, "-n", "-q", ip.String(), "-c", "1", "-W", "1")
		if err != nil {
			// Remote didn't answer
			return
		}

		// Remote answered
		failLock.Lock()
		fail = true
		failLock.Unlock()
	}

	poke := func(ip net.IP) {
		defer wgChecks.Done()

		addr := fmt.Sprintf("%s:22", ip.String())
		if ip.To4() == nil {
			addr = fmt.Sprintf("[%s]:22", ip.String())
		}

		_, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			// Remote answered
			failLock.Lock()
			fail = true
			failLock.Unlock()
			return
		}
	}

	// Ping first IP
	wgChecks.Add(1)
	go ping(GetIP(subnet, 1))

	// Poke port on first IP
	wgChecks.Add(1)
	go poke(GetIP(subnet, 1))

	// Ping check
	if subnet.IP.To4() != nil {
		// Ping last IP
		wgChecks.Add(1)
		go ping(GetIP(subnet, -2))

		// Poke port on last IP
		wgChecks.Add(1)
		go poke(GetIP(subnet, -2))
	}

	wgChecks.Wait()

	return fail
}

// GetHostDevice returns the interface name to use for a combination of parent device name and VLAN ID.
// If no vlan ID supplied, parent name is returned unmodified. If non-empty VLAN ID is supplied then it will look
// for an existing VLAN device and return that, otherwise it will return the default "parent.vlan" format as name.
func GetHostDevice(parent string, vlan string) string {
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

// GetLeaseAddresses returns the lease addresses for a network and hwaddr.
func GetLeaseAddresses(s *state.State, networkName string, hwaddr string) ([]api.InstanceStateNetworkAddress, error) {
	addresses := []api.InstanceStateNetworkAddress{}

	// Look for neighborhood entries for IPv6.
	out, err := shared.RunCommand("ip", "-6", "neigh", "show", "dev", networkName)
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			// Split fields and early validation.
			fields := strings.Fields(line)
			if len(fields) != 4 {
				continue
			}

			if fields[2] != hwaddr {
				continue
			}

			// Prepare the entry.
			addr := api.InstanceStateNetworkAddress{}
			addr.Address = fields[0]
			addr.Family = "inet6"

			if strings.HasPrefix(fields[0], "fe80::") {
				addr.Scope = "link"
			} else {
				addr.Scope = "global"
			}

			addresses = append(addresses, addr)
		}
	}

	// Look for DHCP leases.
	leaseFile := shared.VarPath("networks", networkName, "dnsmasq.leases")
	if !shared.PathExists(leaseFile) {
		return addresses, nil
	}

	dbInfo, err := LoadByName(s, networkName)
	if err != nil {
		return nil, err
	}

	content, err := ioutil.ReadFile(leaseFile)
	if err != nil {
		return nil, err
	}

	for _, lease := range strings.Split(string(content), "\n") {
		fields := strings.Fields(lease)
		if len(fields) < 5 {
			continue
		}

		// Parse the MAC
		mac := GetMACSlice(fields[1])
		macStr := strings.Join(mac, ":")

		if len(macStr) < 17 && fields[4] != "" {
			macStr = fields[4][len(fields[4])-17:]
		}

		if macStr != hwaddr {
			continue
		}

		// Parse the IP
		addr := api.InstanceStateNetworkAddress{
			Address: fields[2],
			Scope:   "global",
		}

		ip := net.ParseIP(addr.Address)
		if ip == nil {
			continue
		}

		if ip.To4() != nil {
			addr.Family = "inet"

			_, subnet, _ := net.ParseCIDR(dbInfo.Config()["ipv4.address"])
			if subnet != nil {
				mask, _ := subnet.Mask.Size()
				addr.Netmask = fmt.Sprintf("%d", mask)
			}
		} else {
			addr.Family = "inet6"

			_, subnet, _ := net.ParseCIDR(dbInfo.Config()["ipv6.address"])
			if subnet != nil {
				mask, _ := subnet.Mask.Size()
				addr.Netmask = fmt.Sprintf("%d", mask)
			}
		}

		addresses = append(addresses, addr)
	}

	return addresses, nil
}

// GetMACSlice parses MAC address.
func GetMACSlice(hwaddr string) []string {
	var buf []string

	if !strings.Contains(hwaddr, ":") {
		if s, err := strconv.ParseUint(hwaddr, 10, 64); err == nil {
			hwaddr = fmt.Sprintln(fmt.Sprintf("%x", s))
			var tuple string
			for i, r := range hwaddr {
				tuple = tuple + string(r)
				if i > 0 && (i+1)%2 == 0 {
					buf = append(buf, tuple)
					tuple = ""
				}
			}
		}
	} else {
		buf = strings.Split(strings.ToLower(hwaddr), ":")
	}

	return buf
}

// UsesIPv4Firewall returns whether network config will need to use the IPv4 firewall.
func UsesIPv4Firewall(netConfig map[string]string) bool {
	if netConfig == nil {
		return false
	}

	if netConfig["ipv4.firewall"] == "" || shared.IsTrue(netConfig["ipv4.firewall"]) {
		return true
	}

	if shared.IsTrue(netConfig["ipv4.nat"]) {
		return true
	}

	return false
}

// UsesIPv6Firewall returns whether network config will need to use the IPv6 firewall.
func UsesIPv6Firewall(netConfig map[string]string) bool {
	if netConfig == nil {
		return false
	}

	if netConfig["ipv6.firewall"] == "" || shared.IsTrue(netConfig["ipv6.firewall"]) {
		return true
	}

	if shared.IsTrue(netConfig["ipv6.nat"]) {
		return true
	}

	return false
}

// BridgeVLANFilteringStatus returns whether VLAN filtering is enabled on a bridge interface.
func BridgeVLANFilteringStatus(interfaceName string) (string, error) {
	content, err := ioutil.ReadFile(fmt.Sprintf("/sys/class/net/%s/bridge/vlan_filtering", interfaceName))
	if err != nil {
		return "", errors.Wrapf(err, "Failed getting bridge VLAN status for %q", interfaceName)
	}

	return strings.TrimSpace(fmt.Sprintf("%s", content)), nil
}

// BridgeVLANFilterSetStatus sets the status of VLAN filtering on a bridge interface.
func BridgeVLANFilterSetStatus(interfaceName string, status string) error {
	err := ioutil.WriteFile(fmt.Sprintf("/sys/class/net/%s/bridge/vlan_filtering", interfaceName), []byte(status), 0)
	if err != nil {
		return errors.Wrapf(err, "Failed enabling VLAN filtering on bridge %q", interfaceName)
	}

	return nil
}

// BridgeVLANDefaultPVID returns the VLAN default port VLAN ID (PVID).
func BridgeVLANDefaultPVID(interfaceName string) (string, error) {
	content, err := ioutil.ReadFile(fmt.Sprintf("/sys/class/net/%s/bridge/default_pvid", interfaceName))
	if err != nil {
		return "", errors.Wrapf(err, "Failed getting bridge VLAN default PVID for %q", interfaceName)
	}

	return strings.TrimSpace(fmt.Sprintf("%s", content)), nil
}

// BridgeVLANSetDefaultPVID sets the VLAN default port VLAN ID (PVID).
func BridgeVLANSetDefaultPVID(interfaceName string, vlanID string) error {
	err := ioutil.WriteFile(fmt.Sprintf("/sys/class/net/%s/bridge/default_pvid", interfaceName), []byte(vlanID), 0)
	if err != nil {
		return errors.Wrapf(err, "Failed setting bridge VLAN default PVID for %q", interfaceName)
	}

	return nil
}
