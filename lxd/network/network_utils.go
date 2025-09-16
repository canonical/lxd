package network

import (
	"bufio"
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"net"
	"net/netip"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/device/nictype"
	"github.com/canonical/lxd/lxd/dnsmasq"
	"github.com/canonical/lxd/lxd/dnsmasq/dhcpalloc"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/ip"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/validate"
	"github.com/canonical/lxd/shared/version"
)

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

// RandomDevName returns a random device name with prefix.
// If the random string combined with the prefix exceeds 13 characters then empty string is returned.
// This is to ensure we support buggy dhclient applications: https://bugs.debian.org/cgi-bin/bugreport.cgi?bug=858580
func RandomDevName(prefix string) string {
	// Return a new random veth device name.
	randBytes := make([]byte, 4)
	_, err := cryptorand.Read(randBytes)
	if err != nil {
		return ""
	}

	iface := prefix + hex.EncodeToString(randBytes)
	if len(iface) > 13 {
		return ""
	}

	return iface
}

// MACDevName returns interface name with prefix 'lxd' and MAC without leading 2 digits.
func MACDevName(mac net.HardwareAddr) string {
	devName := strings.Join(strings.Split(mac.String(), ":"), "")
	return "lxd" + devName[2:]
}

// UsedByInstanceDevices looks for instance NIC devices using the network and runs the supplied usageFunc for each.
// Accepts optional filter arguments to specify a subset of instances.
func UsedByInstanceDevices(s *state.State, networkProjectName string, networkName string, networkType string, usageFunc func(inst db.InstanceArgs, nicName string, nicConfig map[string]string) error, filters ...cluster.InstanceFilter) error {
	// Get the instances.
	projects := map[string]api.Project{}
	var instances []db.InstanceArgs

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.InstanceList(ctx, func(inst db.InstanceArgs, p api.Project) error {
			projects[inst.Project] = p
			instances = append(instances, inst)

			return nil
		}, filters...)
	})
	if err != nil {
		return err
	}

	// Go through the instances and run usageFunc.
	for _, inst := range instances {
		p := projects[inst.Project]

		// Get the instance's effective network project name.
		instNetworkProject := project.NetworkProjectFromRecord(&p)

		// Skip instances who's effective network project doesn't match this Network's project.
		if instNetworkProject != networkProjectName {
			continue
		}

		// Look for NIC devices using this network.
		devices := instancetype.ExpandInstanceDevices(inst.Devices.Clone(), inst.Profiles)
		for devName, devConfig := range devices {
			if isInUseByDevice(networkName, networkType, devConfig) {
				err := usageFunc(inst, devName, devConfig)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// UsedBy returns list of API resources using network. Accepts firstOnly argument to indicate that only the first
// resource using network should be returned. This can help to quickly check if the network is in use.
func UsedBy(s *state.State, networkProjectName string, networkID int64, networkName string, networkType string, firstOnly bool) ([]string, error) {
	var err error
	var usedBy []string

	// If managed network being passed in, check if it has any peerings in a created state.
	if networkID > 0 {
		var peers map[int64]*api.NetworkPeer

		err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			peers, err = tx.GetNetworkPeers(ctx, networkID)

			return err
		})
		if err != nil {
			return nil, fmt.Errorf("Failed getting network peers: %w", err)
		}

		for _, peer := range peers {
			if peer.Status == api.NetworkStatusCreated {
				// Add the target project/network of the peering as using this network.
				usedBy = append(usedBy, api.NewURL().Path(version.APIVersion, "networks", peer.TargetNetwork).Project(peer.TargetProject).String())

				if firstOnly {
					return usedBy, nil
				}
			}
		}
	}

	// Only networks defined in the default project can be used by other networks. Cheapest to do.
	if networkProjectName == api.ProjectDefaultName {
		// Get all managed networks across all projects.
		var projectNetworks map[string]map[int64]api.Network

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			projectNetworks, err = tx.GetCreatedNetworks(ctx)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("Failed to load all networks: %w", err)
		}

		for projectName, networks := range projectNetworks {
			for _, network := range networks {
				if networkName == network.Name && networkProjectName == projectName {
					continue // Skip ourselves.
				}

				// The network's config references the network we are searching for. Either by
				// directly referencing our network or by referencing our interface as its parent.
				if network.Config["network"] == networkName || network.Config["parent"] == networkName {
					usedBy = append(usedBy, api.NewURL().Path(version.APIVersion, "networks", network.Name).Project(projectName).String())

					if firstOnly {
						return usedBy, nil
					}
				}
			}
		}
	}

	// Look for profiles. Next cheapest to do.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get all profiles
		profiles, err := cluster.GetProfiles(ctx, tx.Tx())
		if err != nil {
			return err
		}

		// Get all the profile devices.
		profileDevices, err := cluster.GetDevices(ctx, tx.Tx(), "profile")
		if err != nil {
			return err
		}

		for _, profile := range profiles {
			profileProject, err := cluster.GetProject(ctx, tx.Tx(), profile.Project)
			if err != nil {
				return err
			}

			apiProfileProject, err := profileProject.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			devices := map[string]cluster.Device{}
			for _, dev := range profileDevices[profile.ID] {
				devices[dev.Name] = dev
			}

			inUse, err := usedByProfileDevices(devices, apiProfileProject, networkProjectName, networkName, networkType)
			if err != nil {
				return err
			}

			if inUse {
				usedBy = append(usedBy, api.NewURL().Path(version.APIVersion, "profiles", profile.Name).Project(profile.Project).String())

				if firstOnly {
					return nil
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Check if any instance devices use this network.
	err = UsedByInstanceDevices(s, networkProjectName, networkName, networkType, func(inst db.InstanceArgs, nicName string, nicConfig map[string]string) error {
		usedBy = append(usedBy, api.NewURL().Path(version.APIVersion, "instances", inst.Name).Project(inst.Project).String())

		if firstOnly {
			// No need to consider other devices.
			return db.ErrListStop
		}

		return nil
	})
	if err != nil {
		if err == db.ErrListStop {
			return usedBy, nil
		}

		return nil, err
	}

	return usedBy, nil
}

// usedByProfileDevices indicates if network is referenced by a profile's NIC devices.
// Checks if the device's parent or network properties match the network name.
func usedByProfileDevices(profileDevices map[string]cluster.Device, profileProject *api.Project, networkProjectName string, networkName string, networkType string) (bool, error) {
	// Get the translated network project name from the profiles's project.

	// Skip profiles who's translated network project doesn't match the requested network's project.
	// Because its devices can't be using this network.
	profileNetworkProjectName := project.NetworkProjectFromRecord(profileProject)
	if networkProjectName != profileNetworkProjectName {
		return false, nil
	}

	for _, d := range deviceConfig.NewDevices(cluster.DevicesToAPI(profileDevices)) {
		if isInUseByDevice(networkName, networkType, d) {
			return true, nil
		}
	}

	return false, nil
}

// isInUseByDevices inspects a device's config to find references for a network being used.
func isInUseByDevice(networkName string, networkType string, d deviceConfig.Device) bool {
	if d["type"] != "nic" {
		return false
	}

	if d["network"] != "" && d["network"] == networkName {
		return true
	}

	// OVN networks can only use managed networks.
	if networkType == "ovn" {
		return false
	}

	if d["parent"] != "" && GetHostDevice(d["parent"], d["vlan"]) == networkName {
		return true
	}

	return false
}

// GetDevMTU retrieves the current MTU setting for a named network device.
func GetDevMTU(devName string) (uint32, error) {
	content, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/mtu", devName))
	if err != nil {
		return 0, err
	}

	// Parse value
	mtu, err := strconv.ParseUint(strings.TrimSpace(string(content)), 10, 32)
	if err != nil {
		return 0, err
	}

	return uint32(mtu), nil
}

// GetTXQueueLength retrieves the current txqlen setting for a named network device.
func GetTXQueueLength(devName string) (uint32, error) {
	content, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/tx_queue_len", devName))
	if err != nil {
		return 0, err
	}

	// Parse value
	txqlen, err := strconv.ParseUint(strings.TrimSpace(string(content)), 10, 32)
	if err != nil {
		return 0, err
	}

	return uint32(txqlen), nil
}

// DefaultGatewaySubnetV4 returns subnet of default gateway interface.
func DefaultGatewaySubnetV4() (*net.IPNet, string, error) {
	file, err := os.Open("/proc/net/route")
	if err != nil {
		return nil, "", err
	}

	defer func() { _ = file.Close() }()

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
		return nil, "", errors.New("No default gateway for IPv4")
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
			return nil, "", errors.New("More than one IPv4 subnet on default interface")
		}

		subnet = addrNet
	}

	if subnet == nil {
		return nil, "", errors.New("No IPv4 subnet on default interface")
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

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Pass api.ProjectDefaultName here, as currently dnsmasq (bridged) networks do not support projects.
			networks, err = tx.GetNetworks(ctx, api.ProjectDefaultName)

			return err
		})
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
		for deviceName, d := range inst.ExpandedDevices() {
			// Skip uninteresting entries.
			if d["type"] != "nic" {
				continue
			}

			nicType, err := nictype.NICType(s, inst.Project().Name, d)
			if err != nil || nicType != "bridged" {
				continue
			}

			// Temporarily populate parent from network setting if used.
			if d["network"] != "" {
				d["parent"] = d["network"]
			}

			// Skip devices not connected to managed networks.
			if !slices.Contains(networks, d["parent"]) {
				continue
			}

			// Fill in the hwaddr from volatile.
			d, err = inst.FillNetworkDevice(deviceName, d)
			if err != nil {
				continue
			}

			// Add the new host entries.
			_, ok := entries[d["parent"]]
			if !ok {
				entries[d["parent"]] = [][]string{}
			}

			if (shared.IsTrue(d["security.ipv4_filtering"]) && d["ipv4.address"] == "") || (shared.IsTrue(d["security.ipv6_filtering"]) && d["ipv6.address"] == "") {
				deviceStaticFileName := dnsmasq.StaticAllocationFileName(inst.Project().Name, inst.Name(), deviceName)
				_, curIPv4, curIPv6, err := dnsmasq.DHCPStaticAllocation(d["parent"], deviceStaticFileName)
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

			entries[d["parent"]] = append(entries[d["parent"]], []string{d["hwaddr"], inst.Project().Name, inst.Name(), d["ipv4.address"], d["ipv6.address"], deviceName})
		}
	}

	// Update the host files.
	for _, network := range networks {
		entries := entries[network]

		// Skip networks we don't manage (or don't have DHCP enabled).
		if !shared.PathExists(shared.VarPath("networks", network, "dnsmasq.pid")) {
			continue
		}

		// Pass api.ProjectDefaultName here, as currently dnsmasq (bridged) networks do not support projects.
		n, err := LoadByName(s, api.ProjectDefaultName, network)
		if err != nil {
			return fmt.Errorf("Failed to load network %q in project %q for dnsmasq update: %w", api.ProjectDefaultName, network, err)
		}

		config := n.Config()

		// Wipe everything clean.
		files, err := os.ReadDir(shared.VarPath("networks", network, "dnsmasq.hosts"))
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
			deviceName := entry[5]
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
			err := dnsmasq.UpdateStaticEntry(network, projectName, cName, deviceName, config, hwaddr, ipv4Address, ipv6Address)
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

	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 0 {
			servers = append(servers, fields[0])
		}
	}

	err = scanner.Err()
	if err != nil {
		return servers, err
	}

	return servers, nil
}

func randomSubnetV4() (string, error) {
	for range 100 {
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

	return "", errors.New("Failed to automatically find an unused IPv4 subnet, manual configuration required")
}

func randomSubnetV6() (string, error) {
	for range 100 {
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

	return "", errors.New("Failed to automatically find an unused IPv6 subnet, manual configuration required")
}

// noAvailableAddressErr is used by randomAddressInSubnet to indicate that the subnet was exhausted while searching for
// IP addresses.
type noAvailableAddressErr struct {
	error
}

// Unwrap implements xerrors.Unwrap for noAvailableAddressErr.
func (e noAvailableAddressErr) Unwrap() error {
	return e.error
}

// randomAddressInSubnet finds a random address within a given subnet. If given, the validate function is called for
// each randomly generated value. Each time validate returns false, another IP address will be generated until all
// IPs have been exhausted or the context is cancelled. If no addresses are available in the subnet, a noAvailableAddressErr
// is returned so that this can be detected by the caller.
func randomAddressInSubnet(ctx context.Context, subnet net.IPNet, validate func(net.IP) (bool, error)) (net.IP, error) {
	ones, size := subnet.Mask.Size()
	subnetExponent := size - ones

	// Calculate how many host IPs are available in the subnet.
	usableHostsBig := big.NewInt(0).Exp(big.NewInt(2), big.NewInt(int64(subnetExponent)), nil)

	// Standardise input.
	ip4 := subnet.IP.To4()
	if ip4 != nil {
		subnet.IP = ip4.Mask(subnet.Mask)
		usableHostsBig.Sub(usableHostsBig, big.NewInt(2)) // Remove network and broascast address.
	} else {
		subnet.IP = subnet.IP.To16().Mask(subnet.Mask)
		usableHostsBig.Sub(usableHostsBig, big.NewInt(1)) // Remove network address (IPv6 has no broadcast address).
	}

	// If the subnet only has one address, or if the subnet is IPv4 and has two addresses return the network address if it is valid.
	if subnetExponent == 0 || (ip4 != nil && subnetExponent == 1) {
		if validate != nil {
			isValid, err := validate(subnet.IP)
			if err != nil {
				return nil, err
			} else if !isValid {
				return nil, noAvailableAddressErr{error: fmt.Errorf("No available addresses in subnet %q", subnet.String())}
			}
		}

		return subnet.IP, nil
	}

	// If the subnet is IPv6 and has two addresses, return the second one if it is valid.
	if subnetExponent == 1 {
		candidateIP := big.NewInt(0).Add(big.NewInt(0).SetBytes(subnet.IP), big.NewInt(1)).Bytes()
		if validate != nil {
			isValid, err := validate(candidateIP)
			if err != nil {
				return nil, err
			} else if !isValid {
				return nil, noAvailableAddressErr{error: fmt.Errorf("No available addresses in subnet %q", subnet.String())}
			}
		}

		return candidateIP, nil
	}

	networkAddressInt := big.NewInt(0).SetBytes(subnet.IP)

	// Keep track of attempted IPs so that the validate function isn't repeatedly called in case it's expensive.
	// We don't need to worry about how large this map will grow. It is useful for small subnets where it's likely that
	// all IPs may be exhausted. The max size of a golang map will be the size of int, it is unfeasible for all of those
	// IP addresses to be in use (e.g. with `2001:db8::/32` the resource requirements for 2^32 IP addresses to be in use
	// precludes the possibility of this map growing too large).
	attempted := make(map[[16]byte]struct{})

	for {
		// Return on timeout or context cancellation.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// If we've attempted all possible addresses then return.
		if big.NewInt(int64(len(attempted))).Cmp(usableHostsBig) == 0 {
			return nil, noAvailableAddressErr{error: fmt.Errorf("No available addresses in subnet %q", subnet.String())}
		}

		// Get a random number in range [0, addressRangeSize).
		randBigInt, err := cryptorand.Int(cryptorand.Reader, usableHostsBig)
		if err != nil {
			return nil, err
		}

		// Add the random number to the network address.
		randIPInt := big.NewInt(0).Add(networkAddressInt, randBigInt)

		// Add one to it to exclude the network address.
		randIP := net.IP(big.NewInt(0).Add(randIPInt, big.NewInt(1)).Bytes())
		randIP = randIP.To16()

		// Convert all IPs to a 16 byte representation so that we have a consistent map key we can use for both IPv4 and IPv6.
		var IPKey [16]byte
		copy(IPKey[:], randIP.To16())

		// Retry if we've already attempted this IP.
		_, ok := attempted[IPKey]
		if ok {
			continue
		}

		// Add the IP to the attempted map.
		attempted[IPKey] = struct{}{}

		// Call validate if set.
		if validate != nil {
			isValid, err := validate(randIP)
			if err != nil {
				return nil, err
			} else if !isValid {
				continue
			}
		}

		return randIP, nil
	}
}

func inRoutingTable(subnet *net.IPNet) bool {
	filename := "route"
	if subnet.IP.To4() == nil {
		filename = "ipv6_route"
	}

	file, err := os.Open("/proc/net/" + filename)
	if err != nil {
		return false
	}

	defer func() { _ = file.Close() }()

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
			size, err := strconv.ParseInt(fields[1], 16, 0)
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

// pingIP sends a single ping packet to the specified IP, returns nil error if IP is reachable.
// If ctx doesn't have a deadline then the default timeout used is 1s.
func pingIP(ctx context.Context, ip net.IP) error {
	cmd := "ping"
	if ip.To4() == nil {
		cmd = "ping6"
	}

	timeout := time.Second * 1
	deadline, ok := ctx.Deadline()
	if ok {
		timeout = time.Until(deadline)
	}

	_, err := shared.RunCommandContext(ctx, cmd, "-n", "-q", ip.String(), "-c", "1", "-w", strconv.Itoa(int(timeout.Seconds())))

	return err
}

func pingSubnet(subnet *net.IPNet) bool {
	var fail bool
	var failLock sync.Mutex
	var wgChecks sync.WaitGroup

	ping := func(ip net.IP) {
		defer wgChecks.Done()

		if pingIP(context.TODO(), ip) != nil {
			return
		}

		// Remote answered
		failLock.Lock()
		fail = true
		failLock.Unlock()
	}

	poke := func(ip net.IP) {
		defer wgChecks.Done()

		addr := ip.String() + ":22"
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
	go ping(dhcpalloc.GetIP(subnet, 1))

	// Poke port on first IP
	wgChecks.Add(1)
	go poke(dhcpalloc.GetIP(subnet, 1))

	// Ping check
	if subnet.IP.To4() != nil {
		// Ping last IP
		wgChecks.Add(1)
		go ping(dhcpalloc.GetIP(subnet, -2))

		// Poke port on last IP
		wgChecks.Add(1)
		go poke(dhcpalloc.GetIP(subnet, -2))
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

	defer func() { _ = f.Close() }()

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

// GetNeighbourIPs returns the IP addresses in the neighbour cache for a particular interface and MAC.
func GetNeighbourIPs(interfaceName string, hwaddr net.HardwareAddr) ([]ip.Neigh, error) {
	if hwaddr == nil {
		return nil, nil
	}

	neigh := &ip.Neigh{DevName: interfaceName, MAC: hwaddr}
	neighbours, err := neigh.Show()
	if err != nil {
		return nil, fmt.Errorf("Failed to get IP neighbours for interface %q: %w", interfaceName, err)
	}

	return neighbours, nil
}

// GetLeaseAddresses returns the lease addresses for a network and hwaddr.
func GetLeaseAddresses(networkName string, hwaddr string) ([]net.IP, error) {
	leaseFile := shared.VarPath("networks", networkName, "dnsmasq.leases")
	if !shared.PathExists(leaseFile) {
		return nil, fmt.Errorf("Leases file not found for network %q", networkName)
	}

	content, err := os.ReadFile(leaseFile)
	if err != nil {
		return nil, err
	}

	addresses := []net.IP{}

	for lease := range strings.SplitSeq(string(content), "\n") {
		fields := strings.Fields(lease)
		if len(fields) < 5 {
			continue
		}

		// Parse the MAC.
		mac := GetMACSlice(fields[1])
		macStr := strings.Join(mac, ":")

		if len(macStr) < 17 && fields[4] != "" {
			macStr = fields[4][len(fields[4])-17:]
		}

		if macStr != hwaddr {
			continue
		}

		// Parse the IP.
		ip := net.ParseIP(fields[2])
		if ip != nil {
			addresses = append(addresses, ip)
		}
	}

	return addresses, nil
}

// GetMACSlice parses MAC address.
func GetMACSlice(hwaddr string) []string {
	var buf []string

	if !strings.Contains(hwaddr, ":") {
		s, err := strconv.ParseUint(hwaddr, 10, 64)
		if err == nil {
			hwaddr = fmt.Sprintf("%x\n", s)
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

// usesIPv4Firewall returns whether network config will need to use the IPv4 firewall.
func usesIPv4Firewall(netConfig map[string]string) bool {
	if netConfig == nil {
		return false
	}

	if shared.IsTrueOrEmpty(netConfig["ipv4.firewall"]) {
		return true
	}

	if shared.IsTrue(netConfig["ipv4.nat"]) {
		return true
	}

	return false
}

// usesIPv6Firewall returns whether network config will need to use the IPv6 firewall.
func usesIPv6Firewall(netConfig map[string]string) bool {
	if netConfig == nil {
		return false
	}

	if shared.IsTrueOrEmpty(netConfig["ipv6.firewall"]) {
		return true
	}

	if shared.IsTrue(netConfig["ipv6.nat"]) {
		return true
	}

	return false
}

// RandomHwaddr generates a random MAC address from the provided random source.
func randomHwaddr(r *rand.Rand) string {
	// Generate a new random MAC address using the usual prefix.
	const hex = "0123456789abcdef"

	// Preallocate exact length: "00:16:3e:xx:xx:xx" = 17 chars
	b := [17]byte{'0', '0', ':', '1', '6', ':', '3', 'e'}

	pos := 8
	for range 3 {
		b[pos] = ':'
		b[pos+1] = hex[r.Int31n(16)]
		b[pos+2] = hex[r.Int31n(16)]
		pos += 3
	}

	return string(b[:])
}

// VLANInterfaceCreate creates a VLAN interface on parent interface (if needed).
// Returns boolean indicating if VLAN interface was created.
func VLANInterfaceCreate(parent string, vlanDevice string, vlanID string, gvrp bool) (bool, error) {
	if vlanID == "" {
		return false, nil
	}

	if InterfaceExists(vlanDevice) {
		return false, nil
	}

	// Bring the parent interface up so we can add a vlan to it.
	link := &ip.Link{Name: parent}
	err := link.SetUp()
	if err != nil {
		return false, fmt.Errorf("Failed to bring up parent %q: %w", parent, err)
	}

	vlan := &ip.Vlan{
		Link: ip.Link{
			Name:   vlanDevice,
			Parent: parent,
		},
		VlanID: vlanID,
		Gvrp:   gvrp,
	}

	err = vlan.Add()
	if err != nil {
		return false, fmt.Errorf("Failed to create VLAN interface %q on %q: %w", vlanDevice, parent, err)
	}

	err = vlan.SetUp()
	if err != nil {
		return false, fmt.Errorf("Failed to bring up interface %q: %w", vlanDevice, err)
	}

	// Attempt to disable IPv6 router advertisement acceptance.
	_ = util.SysctlSet(fmt.Sprintf("net/ipv6/conf/%s/accept_ra", vlanDevice), "0")

	// We created a new vlan interface, return true.
	return true, nil
}

// InterfaceRemove removes a network interface by name.
func InterfaceRemove(nic string) error {
	link := &ip.Link{Name: nic}
	err := link.Delete()
	return err
}

// InterfaceExists returns true if network interface exists.
func InterfaceExists(nic string) bool {
	if nic != "" && shared.PathExists("/sys/class/net/"+nic) {
		return true
	}

	return false
}

// IPInSlice returns true if slice has IP element.
func IPInSlice(key net.IP, list []net.IP) bool {
	for _, entry := range list {
		if entry.Equal(key) {
			return true
		}
	}
	return false
}

// SubnetContains returns true if outerSubnet contains innerSubnet.
func SubnetContains(outerSubnet *net.IPNet, innerSubnet *net.IPNet) bool {
	if outerSubnet == nil || innerSubnet == nil {
		return false
	}

	if !outerSubnet.Contains(innerSubnet.IP) {
		return false
	}

	outerOnes, outerBits := outerSubnet.Mask.Size()
	innerOnes, innerBits := innerSubnet.Mask.Size()

	// Check number of bits in mask match.
	if innerBits != outerBits {
		return false
	}

	// Check that the inner subnet isn't outside of the outer subnet.
	if innerOnes < outerOnes {
		return false
	}

	return true
}

// SubnetContainsIP returns true if outsetSubnet contains IP address.
func SubnetContainsIP(outerSubnet *net.IPNet, ip net.IP) bool {
	// Convert ip to ipNet.
	ipIsIP4 := ip.To4() != nil

	prefix := 32
	if !ipIsIP4 {
		prefix = 128
	}

	_, ipSubnet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", ip.String(), prefix))
	if err != nil {
		return false
	}

	ipSubnet.IP = ip

	return SubnetContains(outerSubnet, ipSubnet)
}

// SubnetIterate iterates through each IP in a subnet calling a function for each IP.
// If the ipFunc returns a non-nil error then the iteration stops and the error is returned.
func SubnetIterate(subnet *net.IPNet, ipFunc func(ip net.IP) error) error {
	inc := big.NewInt(1)

	// Convert route start IP to native representations to allow incrementing.
	startIP := subnet.IP.To4()
	if startIP == nil {
		startIP = subnet.IP.To16()
	}

	startBig := big.NewInt(0)
	startBig.SetBytes(startIP)

	// Iterate through IPs in subnet, calling ipFunc for each one.
	for {
		ip := net.IP(startBig.Bytes())
		if !subnet.Contains(ip) {
			break
		}

		err := ipFunc(ip)
		if err != nil {
			return err
		}

		startBig.Add(startBig, inc)
	}

	return nil
}

// SubnetParseAppend parses one or more string CIDR subnets. Appends to the supplied slice. Returns subnets slice.
func SubnetParseAppend(subnets []*net.IPNet, parseSubnet ...string) ([]*net.IPNet, error) {
	for _, subnetStr := range parseSubnet {
		_, subnet, err := net.ParseCIDR(subnetStr)
		if err != nil {
			return nil, fmt.Errorf("Invalid subnet %q: %w", subnetStr, err)
		}

		subnets = append(subnets, subnet)
	}

	return subnets, nil
}

// InterfaceStatus returns the global unicast IP addresses configured on an interface and whether it is up or not.
func InterfaceStatus(nicName string) ([]net.IP, bool, error) {
	iface, err := net.InterfaceByName(nicName)
	if err != nil {
		return nil, false, fmt.Errorf("Failed loading interface %q: %w", nicName, err)
	}

	isUp := iface.Flags&net.FlagUp != 0

	addresses, err := iface.Addrs()
	if err != nil {
		return nil, isUp, fmt.Errorf("Failed getting interface addresses for %q: %w", nicName, err)
	}

	var globalUnicastIPs []net.IP
	for _, address := range addresses {
		ip, _, _ := net.ParseCIDR(address.String())
		if ip == nil {
			continue
		}

		if ip.IsGlobalUnicast() {
			globalUnicastIPs = append(globalUnicastIPs, ip)
		}
	}

	return globalUnicastIPs, isUp, nil
}

// ParsePortRange validates a port range in the form start-end.
func ParsePortRange(r string) (base int64, size int64, err error) {
	entries := strings.Split(r, "-")
	if len(entries) > 2 {
		return -1, -1, fmt.Errorf("Invalid port range %q", r)
	}

	base, err = strconv.ParseInt(entries[0], 10, 64)
	if err != nil {
		return -1, -1, err
	}

	size = int64(1)
	if len(entries) > 1 {
		size, err = strconv.ParseInt(entries[1], 10, 64)
		if err != nil {
			return -1, -1, err
		}

		if size <= base {
			return -1, -1, errors.New("End port should be higher than start port")
		}

		size -= base
		size++
	}

	return base, size, nil
}

// ParseIPToNet parses a standalone IP address into a net.IPNet (with the IP field set to the IP supplied).
// The address family is detected and the subnet size set to /32 for IPv4 or /128 for IPv6.
func ParseIPToNet(ipAddress string) (*net.IPNet, error) {
	subnetSize := 32
	if strings.Contains(ipAddress, ":") {
		subnetSize = 128
	}

	listenAddress, listenAddressNet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", ipAddress, subnetSize))
	if err != nil {
		return nil, err
	}

	listenAddressNet.IP = listenAddress // Add IP back into parsed subnet.

	return listenAddressNet, err
}

// ParseIPCIDRToNet parses an IP in CIDR format into a net.IPNet (with the IP field set to the IP supplied).
func ParseIPCIDRToNet(ipAddressCIDR string) (*net.IPNet, error) {
	listenAddress, listenAddressNet, err := net.ParseCIDR(ipAddressCIDR)
	if err != nil {
		return nil, err
	}

	listenAddressNet.IP = listenAddress // Add IP back into parsed subnet.

	return listenAddressNet, err
}

// IPToNet converts an IP to a single host IPNet.
func IPToNet(ip net.IP) net.IPNet {
	bits := 32
	if ip.To4() == nil {
		bits = 128
	}

	return net.IPNet{
		IP:   ip,
		Mask: net.CIDRMask(bits, bits),
	}
}

// NICUsesNetwork returns true if the nicDev's "network" or "parent" property matches one of the networks names.
func NICUsesNetwork(nicDev map[string]string, networks ...*api.Network) bool {
	for _, network := range networks {
		if network.Name == nicDev["network"] || network.Name == nicDev["parent"] {
			return true
		}
	}

	return false
}

// BridgeNetfilterEnabled checks whether the bridge netfilter feature is loaded and enabled.
// If it is not an error is returned. This is needed in order for instances connected to a bridge to access DNAT
// listeners on the LXD host, as otherwise the packets from the bridge do have the SNAT netfilter rules applied.
func BridgeNetfilterEnabled(ipVersion uint) error {
	sysctlName := "iptables"
	if ipVersion == 6 {
		sysctlName = "ip6tables"
	}

	sysctlPath := "net/bridge/bridge-nf-call-" + sysctlName
	sysctlVal, err := util.SysctlGet(sysctlPath)
	if err != nil {
		return errors.New("br_netfilter kernel module not loaded")
	}

	sysctlVal = strings.TrimSpace(sysctlVal)
	if sysctlVal != "1" {
		return fmt.Errorf("sysctl net.bridge.bridge-nf-call-%s not enabled", sysctlName)
	}

	return nil
}

// ProxyParseAddr validates a proxy address and parses it into its constituent parts.
func ProxyParseAddr(data string) (*deviceConfig.ProxyAddress, error) {
	// Split into <protocol> and <address>.
	fields := strings.SplitN(data, ":", 2)

	if !slices.Contains([]string{"tcp", "udp", "unix"}, fields[0]) {
		return nil, fmt.Errorf("Unknown protocol type %q", fields[0])
	}

	if len(fields) < 2 || fields[1] == "" {
		return nil, errors.New("Missing address")
	}

	newProxyAddr := &deviceConfig.ProxyAddress{
		ConnType: fields[0],
		Abstract: strings.HasPrefix(fields[1], "@"),
	}

	// unix addresses cannot have ports.
	if newProxyAddr.ConnType == "unix" {
		newProxyAddr.Address = fields[1]

		return newProxyAddr, nil
	}

	// Split <address> into <address> and <ports>.
	address, port, err := net.SplitHostPort(fields[1])
	if err != nil {
		return nil, err
	}

	// Validate that it's a valid address.
	if slices.Contains([]string{"udp", "tcp"}, newProxyAddr.ConnType) {
		err := validate.Optional(validate.IsNetworkAddress)(address)
		if err != nil {
			return nil, err
		}
	}

	newProxyAddr.Address = address

	// Split <ports> into individual ports and port ranges.
	ports := strings.Split(port, ",")

	newProxyAddr.Ports = make([]uint64, 0, len(ports))

	for _, p := range ports {
		portFirst, portRange, err := ParsePortRange(p)
		if err != nil {
			return nil, err
		}

		for i := range portRange {
			newProxyAddr.Ports = append(newProxyAddr.Ports, uint64(portFirst+i))
		}
	}

	if len(newProxyAddr.Ports) <= 0 {
		return nil, errors.New("At least one port is required")
	}

	return newProxyAddr, nil
}

// AllowedUplinkNetworks returns a list of allowed networks to use as uplinks based on project restrictions.
func AllowedUplinkNetworks(ctx context.Context, tx *db.ClusterTx, projectConfig map[string]string) ([]string, error) {
	var uplinkNetworkNames []string

	// There are no allowed networks if project is restricted and restricted.networks.uplinks is not set.
	if shared.IsTrue(projectConfig["restricted"]) && projectConfig["restricted.networks.uplinks"] == "" {
		return []string{}, nil
	}

	// Uplink networks are always from the default project.
	networks, err := tx.GetCreatedNetworksByProject(ctx, api.ProjectDefaultName)
	if err != nil {
		return nil, fmt.Errorf("Failed getting uplink networks: %w", err)
	}

	// Add any compatible networks to the uplink network list.
	for _, network := range networks {
		if network.Type == "bridge" || network.Type == "physical" {
			uplinkNetworkNames = append(uplinkNetworkNames, network.Name)
		}
	}

	// If project is not restricted, return full network list.
	if shared.IsFalseOrEmpty(projectConfig["restricted"]) {
		return uplinkNetworkNames, nil
	}

	allowedUplinkNetworkNames := []string{}

	// Parse the allowed uplinks and return any that are present in the actual defined networks.
	allowedRestrictedUplinks := shared.SplitNTrimSpace(projectConfig["restricted.networks.uplinks"], ",", -1, false)

	for _, allowedRestrictedUplink := range allowedRestrictedUplinks {
		if slices.Contains(uplinkNetworkNames, allowedRestrictedUplink) {
			allowedUplinkNetworkNames = append(allowedUplinkNetworkNames, allowedRestrictedUplink)
		}
	}

	return allowedUplinkNetworkNames, nil
}

// complementRangesIP4 returns the complement of the provided IPv4 network ranges.
// Accepts a slice of IPv4 ranges and its network's address as parameters.
// It calculates the IPv4 ranges that are *not* covered by the input slice and
// returns the result.
// Network address is used to find the boundaries of the network (first and last IP),
// this in turn allows the function to consider the full IP space that the ranges belong to.
func complementRangesIP4(ranges []*shared.IPRange, netAddr *net.IPNet) ([]shared.IPRange, error) {
	var complement []shared.IPRange

	// Sort the input slice of IP ranges by their start address from lowest to highest.
	// This is important because it allows us to find gaps within ranges by making a single linear pass
	// over the given ranges.
	sort.Slice(ranges, func(i, j int) bool {
		return bytes.Compare(ranges[i].Start, ranges[j].Start) < 0
	})

	ipv4NetPrefix, err := netip.ParsePrefix(netAddr.String())
	if err != nil {
		return nil, err
	}

	// Initialize a cursor to the start of the network.
	// It tracks the end of the last covered IP range.
	previousEnd := ipv4NetPrefix.Addr()

	// Iterate over the sorted list of given IP ranges to find the gaps between them.
	for _, r := range ranges {
		startAddr, ok := netip.AddrFromSlice(r.Start.To4())
		if !ok {
			return nil, fmt.Errorf("Unable to parse IP %q", r.Start)
		}

		endAddr, ok := netip.AddrFromSlice(r.End.To4())
		if !ok {
			return nil, fmt.Errorf("Unable to parse IP %q", r.End)
		}

		previousEndNext := previousEnd.Next()

		// Check if a gap exists between the last covered range and the current one.
		// A gap is present only if the start of this range comes *after* the IP
		// immediately following the previous end (previousEnd.Next()).
		// This correctly handles adjacent ranges (e.g., ending in .20, starting at .21) by not
		// flagging them as a gap.
		if startAddr.Compare(previousEndNext) == 1 {
			newStart := previousEndNext
			newEnd := startAddr.Prev()
			newRange := shared.IPRange{Start: newStart.AsSlice()}

			// Check if the calculated gap is just a single IP or a multi-IP range.
			if newStart.Compare(newEnd) != 0 {
				// Gap covers multiple IPs so specify an end IP.
				newRange.End = newEnd.AsSlice()
			}

			complement = append(complement, newRange)
		}

		// Advance the cursor to the end of the current range if it extends further
		// than the previous one. This correctly handles overlapping ranges.
		if endAddr.Compare(previousEnd) == 1 {
			previousEnd = endAddr
		}
	}

	broadcastAddr := dhcpalloc.GetIP(netAddr, -1)

	// Set "endAddr" to the end of the network (broadcast address).
	endAddr, ok := netip.AddrFromSlice(broadcastAddr.To4())
	if !ok {
		return nil, fmt.Errorf("Unable to parse IP %q", broadcastAddr)
	}

	// Check for a final gap between the end of the last processed range
	// and the end of the network.
	if previousEnd.Compare(endAddr) == -1 {
		complement = append(complement, shared.IPRange{Start: previousEnd.Next().AsSlice(), End: endAddr.AsSlice()})
	}

	return complement, nil
}

// ipInRanges checks whether the given IP address is contained within any of the
// provided IP network ranges.
func ipInRanges(ipAddr net.IP, ipRanges []shared.IPRange) bool {
	for _, r := range ipRanges {
		containsIP := r.ContainsIP(ipAddr)
		if containsIP {
			return true
		}
	}

	return false
}
