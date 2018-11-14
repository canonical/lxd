package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"math"
	"math/big"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

var networkStaticLock sync.Mutex

func networkAutoAttach(cluster *db.Cluster, devName string) error {
	_, dbInfo, err := cluster.NetworkGetInterface(devName)
	if err != nil {
		// No match found, move on
		return nil
	}

	return networkAttachInterface(dbInfo.Name, devName)
}

func networkAttachInterface(netName string, devName string) error {
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

func networkDetachInterface(netName string, devName string) error {
	if shared.PathExists(fmt.Sprintf("/sys/class/net/%s/bridge", netName)) {
		_, err := shared.RunCommand("ip", "link", "set", "dev", devName, "nomaster")
		if err != nil {
			return err
		}
	} else {
		_, err := shared.RunCommand("ovs-vsctl", "port-to-br", devName)
		if err == nil {
			_, err := shared.RunCommand("ovs-vsctl", "del-port", netName, devName)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func networkGetInterfaces(cluster *db.Cluster) ([]string, error) {
	networks, err := cluster.Networks()
	if err != nil {
		return nil, err
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, iface := range ifaces {
		// Ignore veth pairs (for performance reasons)
		if strings.HasPrefix(iface.Name, "veth") {
			continue
		}

		// Append to the list
		if !shared.StringInSlice(iface.Name, networks) {
			networks = append(networks, iface.Name)
		}
	}

	return networks, nil
}

func networkIsInUse(c container, name string) bool {
	for _, d := range c.ExpandedDevices() {
		if d["type"] != "nic" {
			continue
		}

		if !shared.StringInSlice(d["nictype"], []string{"bridged", "macvlan", "physical", "sriov"}) {
			continue
		}

		if d["parent"] == "" {
			continue
		}

		if networkGetHostDevice(d["parent"], d["vlan"]) == name {
			return true
		}
	}

	return false
}

func networkGetHostDevice(parent string, vlan string) string {
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
		vlanId := strings.TrimSpace(s[1])
		vlanParent := strings.TrimSpace(s[2])

		if vlanParent == parent && vlanId == vlan {
			return vlanIface
		}
	}

	// Return the default pattern
	return defaultVlan
}

func networkGetIP(subnet *net.IPNet, host int64) net.IP {
	// Convert IP to a big int
	bigIP := big.NewInt(0)
	bigIP.SetBytes(subnet.IP.To16())

	// Deal with negative offsets
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

	// Get the new IP int
	bigIP.Add(bigIP, bigCount)

	// Generate an IPv6
	if subnet.IP.To4() == nil {
		newIp := bigIP.Bytes()
		return newIp
	}

	// Generate an IPv4
	newIp := make(net.IP, 4)
	binary.BigEndian.PutUint32(newIp, uint32(bigIP.Int64()))
	return newIp
}

func networkGetTunnels(config map[string]string) []string {
	tunnels := []string{}

	for k := range config {
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

func networkPingSubnet(subnet *net.IPNet) bool {
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
	go ping(networkGetIP(subnet, 1))

	// Poke port on first IP
	wgChecks.Add(1)
	go poke(networkGetIP(subnet, 1))

	// Ping check
	if subnet.IP.To4() != nil {
		// Ping last IP
		wgChecks.Add(1)
		go ping(networkGetIP(subnet, -2))

		// Poke port on last IP
		wgChecks.Add(1)
		go poke(networkGetIP(subnet, -2))
	}

	wgChecks.Wait()

	return fail
}

func networkInRoutingTable(subnet *net.IPNet) bool {
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

func networkRandomSubnetV4() (string, error) {
	for i := 0; i < 100; i++ {
		cidr := fmt.Sprintf("10.%d.%d.1/24", rand.Intn(255), rand.Intn(255))
		_, subnet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}

		if networkInRoutingTable(subnet) {
			continue
		}

		if networkPingSubnet(subnet) {
			continue
		}

		return cidr, nil
	}

	return "", fmt.Errorf("Failed to automatically find an unused IPv4 subnet, manual configuration required")
}

func networkRandomSubnetV6() (string, error) {
	for i := 0; i < 100; i++ {
		cidr := fmt.Sprintf("fd42:%x:%x:%x::1/64", rand.Intn(65535), rand.Intn(65535), rand.Intn(65535))
		_, subnet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}

		if networkInRoutingTable(subnet) {
			continue
		}

		if networkPingSubnet(subnet) {
			continue
		}

		return cidr, nil
	}

	return "", fmt.Errorf("Failed to automatically find an unused IPv6 subnet, manual configuration required")
}

func networkDefaultGatewaySubnetV4() (*net.IPNet, string, error) {
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

func networkValidName(value string) error {
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

func networkValidAddressCIDRV6(value string) error {
	if value == "" {
		return nil
	}

	ip, subnet, err := net.ParseCIDR(value)
	if err != nil {
		return err
	}

	if ip.To4() != nil {
		return fmt.Errorf("Not an IPv6 address: %s", value)
	}

	if ip.String() == subnet.IP.String() {
		return fmt.Errorf("Not a usable IPv6 address: %s", value)
	}

	return nil
}

func networkValidAddressCIDRV4(value string) error {
	if value == "" {
		return nil
	}

	ip, subnet, err := net.ParseCIDR(value)
	if err != nil {
		return err
	}

	if ip.To4() == nil {
		return fmt.Errorf("Not an IPv4 address: %s", value)
	}

	if ip.String() == subnet.IP.String() {
		return fmt.Errorf("Not a usable IPv4 address: %s", value)
	}

	return nil
}

func networkValidAddressV4(value string) error {
	if value == "" {
		return nil
	}

	ip := net.ParseIP(value)
	if ip == nil {
		return fmt.Errorf("Not an IPv4 address: %s", value)
	}

	return nil
}

func networkValidNetworkV4(value string) error {
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

func networkAddressForSubnet(subnet *net.IPNet) (net.IP, string, error) {
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

func networkFanAddress(underlay *net.IPNet, overlay *net.IPNet) (string, string, string, error) {
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
	ip, dev, err := networkAddressForSubnet(underlay)
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

func networkKillForkDNS(name string) error {
	// Check if we have a running forkdns at all
	pidPath := shared.VarPath("networks", name, "forkdns.pid")
	if !shared.PathExists(pidPath) {
		return nil
	}

	// Grab the PID
	content, err := ioutil.ReadFile(pidPath)
	if err != nil {
		return err
	}
	pid := strings.TrimSpace(string(content))

	// Check for empty string
	if pid == "" {
		os.Remove(pidPath)
		return nil
	}

	// Check if it's forkdns
	cmdArgs, err := ioutil.ReadFile(fmt.Sprintf("/proc/%s/cmdline", pid))
	if err != nil {
		os.Remove(pidPath)
		return nil
	}

	cmdFields := strings.Split(string(bytes.TrimRight(cmdArgs, string("\x00"))), string(byte(0)))
	if len(cmdFields) < 5 || cmdFields[1] != "forkdns" {
		os.Remove(pidPath)
		return nil
	}

	// Parse the pid
	pidInt, err := strconv.Atoi(pid)
	if err != nil {
		return err
	}

	// Actually kill the process
	err = syscall.Kill(pidInt, syscall.SIGKILL)
	if err != nil {
		return err
	}

	// Cleanup
	os.Remove(pidPath)
	return nil
}

func networkKillDnsmasq(name string, reload bool) error {
	// Check if we have a running dnsmasq at all
	pidPath := shared.VarPath("networks", name, "dnsmasq.pid")
	if !shared.PathExists(pidPath) {
		if reload {
			return fmt.Errorf("dnsmasq isn't running")
		}

		return nil
	}

	// Grab the PID
	content, err := ioutil.ReadFile(pidPath)
	if err != nil {
		return err
	}
	pid := strings.TrimSpace(string(content))

	// Check for empty string
	if pid == "" {
		os.Remove(pidPath)

		if reload {
			return fmt.Errorf("dnsmasq isn't running")
		}

		return nil
	}

	// Check if the process still exists
	if !shared.PathExists(fmt.Sprintf("/proc/%s", pid)) {
		os.Remove(pidPath)

		if reload {
			return fmt.Errorf("dnsmasq isn't running")
		}

		return nil
	}

	// Check if it's dnsmasq
	cmdPath, err := os.Readlink(fmt.Sprintf("/proc/%s/exe", pid))
	if err != nil {
		cmdPath = ""
	}

	// Deal with deleted paths
	cmdName := filepath.Base(strings.Split(cmdPath, " ")[0])
	if cmdName != "dnsmasq" {
		if reload {
			return fmt.Errorf("dnsmasq isn't running")
		}

		os.Remove(pidPath)
		return nil
	}

	// Parse the pid
	pidInt, err := strconv.Atoi(pid)
	if err != nil {
		return err
	}

	// Actually kill the process
	if reload {
		err = syscall.Kill(pidInt, syscall.SIGHUP)
		if err != nil {
			return err
		}

		return nil
	}

	err = syscall.Kill(pidInt, syscall.SIGKILL)
	if err != nil {
		return err
	}

	// Cleanup
	os.Remove(pidPath)
	return nil
}

func networkGetDnsmasqVersion() (*version.DottedVersion, error) {
	// Discard stderr on purpose (occasional linker errors)
	output, err := exec.Command("dnsmasq", "--version").Output()
	if err != nil {
		return nil, fmt.Errorf("Failed to check dnsmasq version: %v", err)
	}

	lines := strings.Split(string(output), " ")
	return version.NewDottedVersion(lines[2])
}

func networkUpdateStatic(s *state.State, networkName string) error {
	// We don't want to race with ourselves here
	networkStaticLock.Lock()
	defer networkStaticLock.Unlock()

	// Get all the networks
	var networks []string
	if networkName == "" {
		var err error
		networks, err = s.Cluster.Networks()
		if err != nil {
			return err
		}
	} else {
		networks = []string{networkName}
	}

	// Get all the containers
	containers, err := containerLoadNodeAll(s)
	if err != nil {
		return err
	}

	// Build a list of dhcp host entries
	entries := map[string][][]string{}
	for _, c := range containers {
		// Go through all its devices (including profiles
		for k, d := range c.ExpandedDevices() {
			// Skip uninteresting entries
			if d["type"] != "nic" || d["nictype"] != "bridged" || !shared.StringInSlice(d["parent"], networks) {
				continue
			}

			// Fill in the hwaddr from volatile
			d, err = c.(*containerLXC).fillNetworkDevice(k, d)
			if err != nil {
				continue
			}

			// Add the new host entries
			_, ok := entries[d["parent"]]
			if !ok {
				entries[d["parent"]] = [][]string{}
			}

			entries[d["parent"]] = append(entries[d["parent"]], []string{d["hwaddr"], projectPrefix(c.Project(), c.Name()), d["ipv4.address"], d["ipv6.address"]})
		}
	}

	// Update the host files
	for _, network := range networks {
		entries, _ := entries[network]

		// Skip networks we don't manage (or don't have DHCP enabled)
		if !shared.PathExists(shared.VarPath("networks", network, "dnsmasq.pid")) {
			continue
		}

		n, err := networkLoadByName(s, network)
		if err != nil {
			return err
		}
		config := n.Config()

		// Wipe everything clean
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

		// Apply the changes
		for entryIdx, entry := range entries {
			hwaddr := entry[0]
			cName := entry[1]
			ipv4Address := entry[2]
			ipv6Address := entry[3]
			line := hwaddr

			// Look for duplicates
			duplicate := false
			for iIdx, i := range entries {
				if entry[1] == i[1] {
					// Skip ourselves
					continue
				}

				if entry[0] == i[0] {
					// Find broken configurations
					logger.Errorf("Duplicate MAC detected: %s and %s", entry[1], i[1])
				}

				if i[2] == "" && i[3] == "" {
					// Skip unconfigured
					continue
				}

				if entry[2] == i[2] && entry[3] == i[3] {
					// Find identical containers (copies with static configuration)
					if entryIdx > iIdx {
						duplicate = true
					} else {
						line = fmt.Sprintf("%s,%s", line, i[0])
						logger.Debugf("Found containers with duplicate IPv4/IPv6: %s and %s", entry[1], i[1])
					}
				}
			}

			if duplicate {
				continue
			}

			// Generate the dhcp-host line
			if ipv4Address != "" {
				line += fmt.Sprintf(",%s", ipv4Address)
			}

			if ipv6Address != "" {
				line += fmt.Sprintf(",[%s]", ipv6Address)
			}

			if config["dns.mode"] == "" || config["dns.mode"] == "managed" {
				line += fmt.Sprintf(",%s", cName)
			}

			if line == hwaddr {
				continue
			}

			err := ioutil.WriteFile(shared.VarPath("networks", network, "dnsmasq.hosts", cName), []byte(line+"\n"), 0644)
			if err != nil {
				return err
			}
		}

		// Signal dnsmasq
		err = networkKillDnsmasq(network, true)
		if err != nil {
			return err
		}
	}

	return nil
}

func networkSysctl(path string, value string) error {
	content, err := ioutil.ReadFile(fmt.Sprintf("/proc/sys/net/%s", path))
	if err != nil {
		return err
	}

	if strings.TrimSpace(string(content)) == value {
		return nil
	}

	return ioutil.WriteFile(fmt.Sprintf("/proc/sys/net/%s", path), []byte(value), 0)
}

func networkGetMacSlice(hwaddr string) []string {
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

func networkClearLease(s *state.State, name string, network string, hwaddr string) error {
	leaseFile := shared.VarPath("networks", network, "dnsmasq.leases")

	// Check that we are in fact running a dnsmasq for the network
	if !shared.PathExists(leaseFile) {
		return nil
	}

	// Restart the network when we're done here
	n, err := networkLoadByName(s, network)
	if err != nil {
		return err
	}
	defer n.Start()

	// Stop dnsmasq
	err = networkKillDnsmasq(network, false)
	if err != nil {
		return err
	}

	// Mangle the lease file
	leases, err := ioutil.ReadFile(leaseFile)
	if err != nil {
		return err
	}

	fd, err := os.Create(leaseFile)
	if err != nil {
		return err
	}

	knownMac := networkGetMacSlice(hwaddr)
	for _, lease := range strings.Split(string(leases), "\n") {
		if lease == "" {
			continue
		}

		fields := strings.Fields(lease)
		if len(fields) > 2 {
			if strings.Contains(fields[1], ":") {
				leaseMac := networkGetMacSlice(fields[1])
				leaseMacStr := strings.Join(leaseMac, ":")

				knownMacStr := strings.Join(knownMac[len(knownMac)-len(leaseMac):], ":")
				if knownMacStr == leaseMacStr {
					continue
				}
			} else if len(fields) > 3 && fields[3] == name {
				// Mostly IPv6 leases which don't contain a MAC address...
				continue
			}
		}

		_, err := fd.WriteString(fmt.Sprintf("%s\n", lease))
		if err != nil {
			return err
		}
	}

	err = fd.Close()
	if err != nil {
		return err
	}

	return nil
}

func networkGetState(netIf net.Interface) api.NetworkState {
	netState := "down"
	netType := "unknown"

	if netIf.Flags&net.FlagBroadcast > 0 {
		netType = "broadcast"
	}

	if netIf.Flags&net.FlagPointToPoint > 0 {
		netType = "point-to-point"
	}

	if netIf.Flags&net.FlagLoopback > 0 {
		netType = "loopback"
	}

	if netIf.Flags&net.FlagUp > 0 {
		netState = "up"
	}

	network := api.NetworkState{
		Addresses: []api.NetworkStateAddress{},
		Counters:  api.NetworkStateCounters{},
		Hwaddr:    netIf.HardwareAddr.String(),
		Mtu:       netIf.MTU,
		State:     netState,
		Type:      netType,
	}

	// Get address information
	addrs, err := netIf.Addrs()
	if err == nil {
		for _, addr := range addrs {
			fields := strings.SplitN(addr.String(), "/", 2)
			if len(fields) != 2 {
				continue
			}

			family := "inet"
			if strings.Contains(fields[0], ":") {
				family = "inet6"
			}

			scope := "global"
			if strings.HasPrefix(fields[0], "127") {
				scope = "local"
			}

			if fields[0] == "::1" {
				scope = "local"
			}

			if strings.HasPrefix(fields[0], "169.254") {
				scope = "link"
			}

			if strings.HasPrefix(fields[0], "fe80:") {
				scope = "link"
			}

			address := api.NetworkStateAddress{}
			address.Family = family
			address.Address = fields[0]
			address.Netmask = fields[1]
			address.Scope = scope

			network.Addresses = append(network.Addresses, address)
		}
	}

	// Get counters
	network.Counters = shared.NetworkGetCounters(netIf.Name)
	return network
}
