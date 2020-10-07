package network

import (
	"fmt"
	"hash/fnv"
	"io"
	"io/ioutil"
	"math/big"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/mdlayher/netx/eui64"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/locking"
	"github.com/lxc/lxd/lxd/network/openvswitch"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/validate"
)

const ovnChassisPriorityMax = 32767
const ovnVolatileParentIPv4 = "volatile.network.ipv4.address"
const ovnVolatileParentIPv6 = "volatile.network.ipv6.address"

// ovnParentVars OVN object variables derived from parent network.
type ovnParentVars struct {
	// Router.
	routerExtPortIPv4Net string
	routerExtPortIPv6Net string
	routerExtGwIPv4      net.IP
	routerExtGwIPv6      net.IP

	// External Switch.
	extSwitchProviderName string

	// DNS.
	dnsIPv6 []net.IP
	dnsIPv4 []net.IP
}

// ovnParentPortBridgeVars parent bridge port variables used for start/stop.
type ovnParentPortBridgeVars struct {
	ovsBridge string
	parentEnd string
	ovsEnd    string
}

// ovn represents a LXD OVN network.
type ovn struct {
	common
}

// Type returns the network type.
func (n *ovn) Type() string {
	return "ovn"
}

// DBType returns the network type DB ID.
func (n *ovn) DBType() db.NetworkType {
	return db.NetworkTypeOVN
}

// Config returns the network driver info.
func (n *ovn) Info() Info {
	return Info{
		Projects:           true,
		NodeSpecificConfig: false,
	}
}

// Validate network config.
func (n *ovn) Validate(config map[string]string) error {
	rules := map[string]func(value string) error{
		"network":       validate.IsAny, // Is validated during setup.
		"bridge.hwaddr": validate.Optional(validate.IsNetworkMAC),
		"bridge.mtu":    validate.Optional(validate.IsNetworkMTU),
		"ipv4.address": func(value string) error {
			if validate.IsOneOf(value, []string{"auto"}) == nil {
				return nil
			}

			return validate.Optional(validate.IsNetworkAddressCIDRV4)(value)
		},
		"ipv6.address": func(value string) error {
			if validate.IsOneOf(value, []string{"auto"}) == nil {
				return nil
			}

			return validate.Optional(validate.IsNetworkAddressCIDRV6)(value)
		},
		"ipv6.dhcp.stateful": validate.Optional(validate.IsBool),
		"dns.domain":         validate.IsAny,
		"dns.search":         validate.IsAny,

		// Volatile keys populated automatically as needed.
		ovnVolatileParentIPv4: validate.Optional(validate.IsNetworkAddressV4),
		ovnVolatileParentIPv6: validate.Optional(validate.IsNetworkAddressV6),
	}

	err := n.validate(config, rules)
	if err != nil {
		return err
	}

	return nil
}

// getClient initialises OVN client and returns it.
func (n *ovn) getClient() (*openvswitch.OVN, error) {
	nbConnection, err := cluster.ConfigGetString(n.state.Cluster, "network.ovn.northbound_connection")
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to get OVN northbound connection string")
	}

	client := openvswitch.NewOVN()
	client.SetDatabaseAddress(nbConnection)

	return client, nil
}

// getBridgeMTU returns MTU that should be used for the bridge and instance devices.
// Will also be used to configure the OVN DHCP and IPv6 RA options. Returns 0 if the bridge.mtu is not set/invalid.
func (n *ovn) getBridgeMTU() uint32 {
	if n.config["bridge.mtu"] != "" {
		mtu, err := strconv.ParseUint(n.config["bridge.mtu"], 10, 32)
		if err != nil {
			return 0
		}

		return uint32(mtu)
	}

	return 0
}

// getUnderlayInfo returns the MTU for the underlay network interface and the enscapsulation IP for OVN tunnels.
func (n *ovn) getUnderlayInfo() (uint32, net.IP, error) {
	// findMTUFromIP searches all interfaces on the host looking for one that has specified IP.
	findMTUFromIP := func(findIP net.IP) (uint32, error) {
		// Look for interface that has the OVN enscapsulation IP assigned.
		ifaces, err := net.Interfaces()
		if err != nil {
			return 0, errors.Wrapf(err, "Failed getting local network interfaces")
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

				if ip.Equal(findIP) {
					underlayMTU, err := GetDevMTU(iface.Name)
					if err != nil {
						return 0, errors.Wrapf(err, "Failed getting MTU for %q", iface.Name)
					}

					return underlayMTU, nil // Found what we were looking for.
				}
			}
		}

		return 0, fmt.Errorf("No matching interface found for OVN enscapsulation IP %q", findIP.String())
	}

	ovs := openvswitch.NewOVS()
	encapIP, err := ovs.OVNEncapIP()
	if err != nil {
		return 0, nil, errors.Wrapf(err, "Failed getting OVN enscapsulation IP from OVS")
	}

	underlayMTU, err := findMTUFromIP(encapIP)
	if err != nil {
		return 0, nil, err
	}

	return underlayMTU, encapIP, nil
}

// getOptimalBridgeMTU returns the MTU that can be used for the bridge and instance devices based on the MTU value
// of the OVN underlay network interface. This assumes that the OVN tunnel mechanism used is geneve and that the
// same underlying network settings (MTU and encapsulation IP family) are used on all OVN nodes.
func (n *ovn) getOptimalBridgeMTU() (uint32, error) {
	// Get underlay MTU and encapsulation IP.
	underlayMTU, encapIP, err := n.getUnderlayInfo()
	if err != nil {
		return 0, errors.Wrapf(err, "Failed getting OVN underlay info")
	}

	// Encapsulation family is IPv6.
	if encapIP.To4() == nil {
		// If the underlay's MTU is large enough to accommodate a 1500 overlay MTU and the geneve tunnel
		// overhead of 78 bytes (when used with IPv6 encapsulation) then indicate 1500 MTU can be used.
		if underlayMTU >= 1578 {
			return 1500, nil
		}

		// Default to 1422 which can work with an underlay MTU of 1500.
		return 1422, nil
	}

	// If the underlay's MTU is large enough to accommodate a 1500 overlay MTU and the geneve tunnel
	// overhead of 58 bytes (when used with IPv4 encapsulation) then indicate 1500 MTU can be used.
	if underlayMTU >= 1558 {
		return 1500, nil
	}

	// Default to 1442 which can work with underlay MTU of 1500.
	return 1442, nil
}

// getNetworkPrefix returns OVN network prefix to use for object names.
func (n *ovn) getNetworkPrefix() string {
	return fmt.Sprintf("lxd-net%d", n.id)
}

// getChassisGroup returns OVN chassis group name to use.
func (n *ovn) getChassisGroupName() openvswitch.OVNChassisGroup {
	return openvswitch.OVNChassisGroup(n.getNetworkPrefix())
}

// getRouterName returns OVN logical router name to use.
func (n *ovn) getRouterName() openvswitch.OVNRouter {
	return openvswitch.OVNRouter(fmt.Sprintf("%s-lr", n.getNetworkPrefix()))
}

// getRouterExtPortName returns OVN logical router external port name to use.
func (n *ovn) getRouterExtPortName() openvswitch.OVNRouterPort {
	return openvswitch.OVNRouterPort(fmt.Sprintf("%s-lrp-ext", n.getRouterName()))
}

// getRouterIntPortName returns OVN logical router internal port name to use.
func (n *ovn) getRouterIntPortName() openvswitch.OVNRouterPort {
	return openvswitch.OVNRouterPort(fmt.Sprintf("%s-lrp-int", n.getRouterName()))
}

// getRouterMAC returns OVN router MAC address to use for ports. Uses a stable seed to return stable random MAC.
func (n *ovn) getRouterMAC() (net.HardwareAddr, error) {
	hwAddr := n.config["bridge.hwaddr"]
	if hwAddr == "" {
		// Load server certificate. This is needs to be the same certificate for all nodes in a cluster.
		cert, err := util.LoadCert(n.state.OS.VarDir)
		if err != nil {
			return nil, err
		}

		// Generate the random seed, this uses the server certificate fingerprint (to ensure that multiple
		// standalone nodes on the same external network don't generate the same MAC for their networks).
		// It relies on the certificate being the same for all nodes in a cluster to allow the same MAC to
		// be generated on each bridge interface in the network.
		seed := fmt.Sprintf("%s.%d.%d", cert.Fingerprint(), 0, n.ID())

		// Generate a hash from the randSourceNodeID and network ID to use as seed for random MAC.
		// Use the FNV-1a hash algorithm to convert our seed string into an int64 for use as seed.
		hash := fnv.New64a()
		_, err = io.WriteString(hash, seed)
		if err != nil {
			return nil, err
		}

		// Initialise a non-cryptographic random number generator using the stable seed.
		r := rand.New(rand.NewSource(int64(hash.Sum64())))
		hwAddr = randomHwaddr(r)
		n.logger.Debug("Stable MAC generated", log.Ctx{"seed": seed, "hwAddr": hwAddr})
	}

	mac, err := net.ParseMAC(hwAddr)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed parsing router MAC address %q", mac)
	}

	return mac, nil
}

// getRouterIntPortIPv4Net returns OVN logical router internal port IPv4 address and subnet.
func (n *ovn) getRouterIntPortIPv4Net() string {
	return n.config["ipv4.address"]
}

// getRouterIntPortIPv4Net returns OVN logical router internal port IPv6 address and subnet.
func (n *ovn) getRouterIntPortIPv6Net() string {
	return n.config["ipv6.address"]
}

// getDomainName returns OVN DHCP domain name.
func (n *ovn) getDomainName() string {
	if n.config["dns.domain"] != "" {
		return n.config["dns.domain"]
	}

	return "lxd"
}

// getDNSSearchList returns OVN DHCP DNS search list. If no search list set returns getDomainName() as list.
func (n *ovn) getDNSSearchList() []string {
	if n.config["dns.search"] != "" {
		dnsSearchList := []string{}
		for _, domain := range strings.SplitN(n.config["dns.search"], ",", -1) {
			dnsSearchList = append(dnsSearchList, strings.TrimSpace(domain))
		}

		return dnsSearchList
	}

	return []string{n.getDomainName()}
}

// getExtSwitchName returns OVN  logical external switch name.
func (n *ovn) getExtSwitchName() openvswitch.OVNSwitch {
	return openvswitch.OVNSwitch(fmt.Sprintf("%s-ls-ext", n.getNetworkPrefix()))
}

// getExtSwitchRouterPortName returns OVN logical external switch router port name.
func (n *ovn) getExtSwitchRouterPortName() openvswitch.OVNSwitchPort {
	return openvswitch.OVNSwitchPort(fmt.Sprintf("%s-lsp-router", n.getExtSwitchName()))
}

// getExtSwitchProviderPortName returns OVN logical external switch provider port name.
func (n *ovn) getExtSwitchProviderPortName() openvswitch.OVNSwitchPort {
	return openvswitch.OVNSwitchPort(fmt.Sprintf("%s-lsp-provider", n.getExtSwitchName()))
}

// getIntSwitchName returns OVN logical internal switch name.
func (n *ovn) getIntSwitchName() openvswitch.OVNSwitch {
	return openvswitch.OVNSwitch(fmt.Sprintf("%s-ls-int", n.getNetworkPrefix()))
}

// getIntSwitchRouterPortName returns OVN logical internal switch router port name.
func (n *ovn) getIntSwitchRouterPortName() openvswitch.OVNSwitchPort {
	return openvswitch.OVNSwitchPort(fmt.Sprintf("%s-lsp-router", n.getIntSwitchName()))
}

// getIntSwitchInstancePortPrefix returns OVN logical internal switch instance port name prefix.
func (n *ovn) getIntSwitchInstancePortPrefix() string {
	return fmt.Sprintf("%s-instance", n.getNetworkPrefix())
}

// setupParentPort initialises the parent uplink connection. Returns the derived ovnParentVars settings used
// during the initial creation of the logical network.
func (n *ovn) setupParentPort(routerMAC net.HardwareAddr) (*ovnParentVars, error) {
	// Parent network must be in default project.
	parentNet, err := LoadByName(n.state, project.Default, n.config["network"])
	if err != nil {
		return nil, errors.Wrapf(err, "Failed loading parent network %q", n.config["network"])
	}

	switch parentNet.Type() {
	case "bridge":
		return n.setupParentPortBridge(parentNet, routerMAC)
	}

	return nil, fmt.Errorf("Failed setting up parent port, network type %q unsupported as OVN parent", parentNet.Type())
}

// setupParentPortBridge allocates external IPs on the parent bridge.
// Returns the derived ovnParentVars settings.
func (n *ovn) setupParentPortBridge(parentNet Network, routerMAC net.HardwareAddr) (*ovnParentVars, error) {
	bridgeNet, ok := parentNet.(*bridge)
	if !ok {
		return nil, fmt.Errorf("Network is not bridge type")
	}

	err := bridgeNet.checkClusterWideMACSafe(bridgeNet.config)
	if err != nil {
		return nil, errors.Wrapf(err, "Network %q is not suitable for use as OVN parent", bridgeNet.name)
	}

	v, err := n.allocateParentPortIPs(parentNet, routerMAC)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed allocating parent port IPs on network %q", parentNet.Name())
	}

	return v, nil
}

// allocateParentPortIPs attempts to find a free IP in the parent network's OVN ranges and then stores it in
// ovnVolatileParentIPv4 and ovnVolatileParentIPv6 config keys on this network. Returns ovnParentVars settings.
func (n *ovn) allocateParentPortIPs(parentNet Network, routerMAC net.HardwareAddr) (*ovnParentVars, error) {
	v := &ovnParentVars{}

	parentNetConf := parentNet.Config()

	// Parent derived settings.
	v.extSwitchProviderName = parentNet.Name()

	// Detect parent gateway setting.
	parentIPv4CIDR := parentNetConf["ipv4.address"]
	if parentIPv4CIDR == "" {
		parentIPv4CIDR = parentNetConf["ipv4.gateway"]
	}

	parentIPv6CIDR := parentNetConf["ipv6.address"]
	if parentIPv6CIDR == "" {
		parentIPv6CIDR = parentNetConf["ipv6.gateway"]
	}

	// Optional parent values.
	parentIPv4, parentIPv4Net, err := net.ParseCIDR(parentIPv4CIDR)
	if err == nil {
		v.dnsIPv4 = []net.IP{parentIPv4}
		v.routerExtGwIPv4 = parentIPv4
	}

	parentIPv6, parentIPv6Net, err := net.ParseCIDR(parentIPv6CIDR)
	if err == nil {
		v.dnsIPv6 = []net.IP{parentIPv6}
		v.routerExtGwIPv6 = parentIPv6
	}

	// Detect optional DNS server list.
	if parentNetConf["dns.nameservers"] != "" {
		// Reset nameservers.
		v.dnsIPv4 = nil
		v.dnsIPv6 = nil

		nsList := strings.Split(parentNetConf["dns.nameservers"], ",")
		for _, ns := range nsList {
			nsIP := net.ParseIP(strings.TrimSpace(ns))
			if nsIP == nil {
				return nil, fmt.Errorf("Invalid parent nameserver")
			}

			if nsIP.To4() == nil {
				v.dnsIPv6 = append(v.dnsIPv6, nsIP)
			} else {
				v.dnsIPv4 = append(v.dnsIPv4, nsIP)
			}
		}
	}

	// Parse existing allocated IPs for this network on the parent network (if not set yet, will be nil).
	routerExtPortIPv4 := net.ParseIP(n.config[ovnVolatileParentIPv4])
	routerExtPortIPv6 := net.ParseIP(n.config[ovnVolatileParentIPv6])

	// Decide whether we need to allocate new IP(s) and go to the expense of retrieving all allocated IPs.
	if (parentIPv4Net != nil && routerExtPortIPv4 == nil) || (parentIPv6Net != nil && routerExtPortIPv6 == nil) {
		err := n.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
			allAllocatedIPv4, allAllocatedIPv6, err := n.parentAllAllocatedIPs(tx, parentNet.Name())
			if err != nil {
				return errors.Wrapf(err, "Failed to get all allocated IPs for parent")
			}

			if parentIPv4Net != nil && routerExtPortIPv4 == nil {
				if parentNetConf["ipv4.ovn.ranges"] == "" {
					return fmt.Errorf(`Missing required "ipv4.ovn.ranges" config key on parent network`)
				}

				ipRanges, err := parseIPRanges(parentNetConf["ipv4.ovn.ranges"], parentNet.DHCPv4Subnet())
				if err != nil {
					return errors.Wrapf(err, "Failed to parse parent IPv4 OVN ranges")
				}

				routerExtPortIPv4, err = n.parentAllocateIP(ipRanges, allAllocatedIPv4)
				if err != nil {
					return errors.Wrapf(err, "Failed to allocate parent IPv4 address")
				}

				n.config[ovnVolatileParentIPv4] = routerExtPortIPv4.String()
			}

			if parentIPv6Net != nil && routerExtPortIPv6 == nil {
				// If IPv6 OVN ranges are specified by the parent, allocate from them.
				if parentNetConf["ipv6.ovn.ranges"] != "" {
					ipRanges, err := parseIPRanges(parentNetConf["ipv6.ovn.ranges"], parentNet.DHCPv6Subnet())
					if err != nil {
						return errors.Wrapf(err, "Failed to parse parent IPv6 OVN ranges")
					}

					routerExtPortIPv6, err = n.parentAllocateIP(ipRanges, allAllocatedIPv6)
					if err != nil {
						return errors.Wrapf(err, "Failed to allocate parent IPv6 address")
					}

				} else {
					// Otherwise use EUI64 derived from MAC address.
					routerExtPortIPv6, err = eui64.ParseMAC(parentIPv6Net.IP, routerMAC)
					if err != nil {
						return err
					}
				}

				n.config[ovnVolatileParentIPv6] = routerExtPortIPv6.String()
			}

			err = tx.UpdateNetwork(n.id, n.description, n.config)
			if err != nil {
				return errors.Wrapf(err, "Failed saving allocated parent network IPs")
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// Configure variables needed to configure OVN router.
	if parentIPv4Net != nil && routerExtPortIPv4 != nil {
		routerExtPortIPv4Net := &net.IPNet{
			Mask: parentIPv4Net.Mask,
			IP:   routerExtPortIPv4,
		}
		v.routerExtPortIPv4Net = routerExtPortIPv4Net.String()
	}

	if parentIPv6Net != nil {
		routerExtPortIPv6Net := &net.IPNet{
			Mask: parentIPv6Net.Mask,
			IP:   routerExtPortIPv6,
		}
		v.routerExtPortIPv6Net = routerExtPortIPv6Net.String()
	}

	return v, nil
}

// parentAllAllocatedIPs gets a list of all IPv4 and IPv6 addresses allocated to OVN networks connected to parent.
func (n *ovn) parentAllAllocatedIPs(tx *db.ClusterTx, parentNetName string) ([]net.IP, []net.IP, error) {
	// Get all managed networks across all projects.
	projectNetworks, err := tx.GetNonPendingNetworks()
	if err != nil {
		return nil, nil, errors.Wrapf(err, "Failed to load all networks")
	}

	v4IPs := make([]net.IP, 0)
	v6IPs := make([]net.IP, 0)

	for _, networks := range projectNetworks {
		for _, netInfo := range networks {
			if netInfo.Type != "ovn" || netInfo.Config["network"] != parentNetName {
				continue
			}

			for _, k := range []string{ovnVolatileParentIPv4, ovnVolatileParentIPv6} {
				if netInfo.Config[k] != "" {
					ip := net.ParseIP(netInfo.Config[k])
					if ip != nil {
						if ip.To4() != nil {
							v4IPs = append(v4IPs, ip)
						} else {
							v6IPs = append(v6IPs, ip)
						}
					}
				}
			}
		}
	}

	return v4IPs, v6IPs, nil
}

// parentAllocateIP allocates a free IP from one of the IP ranges.
func (n *ovn) parentAllocateIP(ipRanges []*shared.IPRange, allAllocated []net.IP) (net.IP, error) {
	for _, ipRange := range ipRanges {
		inc := big.NewInt(1)

		// Convert IPs in range to native representations to allow incrementing and comparison.
		startIP := ipRange.Start.To4()
		if startIP == nil {
			startIP = ipRange.Start.To16()
		}

		endIP := ipRange.End.To4()
		if endIP == nil {
			endIP = ipRange.End.To16()
		}

		startBig := big.NewInt(0)
		startBig.SetBytes(startIP)
		endBig := big.NewInt(0)
		endBig.SetBytes(endIP)

		// Iterate through IPs in range, return the first unallocated one found.
		for {
			if startBig.Cmp(endBig) > 0 {
				break
			}

			ip := net.IP(startBig.Bytes())

			// Check IP is not already allocated.
			freeIP := true
			for _, allocatedIP := range allAllocated {
				if ip.Equal(allocatedIP) {
					freeIP = false
					break
				}

			}

			if !freeIP {
				startBig.Add(startBig, inc)
				continue
			}

			return ip, nil
		}
	}

	return nil, fmt.Errorf("No free IPs available")
}

// startParentPort performs any network start up logic needed to connect the parent uplink connection to OVN.
func (n *ovn) startParentPort() error {
	// Parent network must be in default project.
	parentNet, err := LoadByName(n.state, project.Default, n.config["network"])
	if err != nil {
		return errors.Wrapf(err, "Failed loading parent network")
	}

	// Lock parent network so that if multiple OVN networks are trying to connect to the same parent we don't
	// race each other setting up the connection.
	unlock := locking.Lock(n.parentOperationLockName(parentNet))
	defer unlock()

	switch parentNet.Type() {
	case "bridge":
		return n.startParentPortBridge(parentNet)
	}

	return fmt.Errorf("Failed starting parent port, network type %q unsupported as OVN parent", parentNet.Type())
}

// parentOperationLockName returns the lock name to use for operations on the parent network.
func (n *ovn) parentOperationLockName(parentNet Network) string {
	return fmt.Sprintf("network.ovn.%s", parentNet.Name())
}

// parentPortBridgeVars returns the parent port bridge variables needed for port start/stop.
func (n *ovn) parentPortBridgeVars(parentNet Network) *ovnParentPortBridgeVars {
	ovsBridge := fmt.Sprintf("lxdovn%d", parentNet.ID())

	return &ovnParentPortBridgeVars{
		ovsBridge: ovsBridge,
		parentEnd: fmt.Sprintf("%sa", ovsBridge),
		ovsEnd:    fmt.Sprintf("%sb", ovsBridge),
	}
}

// startParentPortBridge creates veth pair (if doesn't exist), creates OVS bridge (if doesn't exist) and
// connects veth pair to parent bridge and OVS bridge.
func (n *ovn) startParentPortBridge(parentNet Network) error {
	vars := n.parentPortBridgeVars(parentNet)

	// Do this after gaining lock so that on failure we revert before release locking.
	revert := revert.New()
	defer revert.Fail()

	// Create veth pair if needed.
	if !InterfaceExists(vars.parentEnd) && !InterfaceExists(vars.ovsEnd) {
		_, err := shared.RunCommand("ip", "link", "add", "dev", vars.parentEnd, "type", "veth", "peer", "name", vars.ovsEnd)
		if err != nil {
			return errors.Wrapf(err, "Failed to create the uplink veth interfaces %q and %q", vars.parentEnd, vars.ovsEnd)
		}

		revert.Add(func() { shared.RunCommand("ip", "link", "delete", vars.parentEnd) })
	}

	// Ensure that the veth interfaces inherit the uplink bridge's MTU (which the OVS bridge also inherits).
	parentNetConfig := parentNet.Config()
	if parentNetConfig["bridge.mtu"] != "" {
		err := InterfaceSetMTU(vars.parentEnd, parentNetConfig["bridge.mtu"])
		if err != nil {
			return err
		}

		err = InterfaceSetMTU(vars.ovsEnd, parentNetConfig["bridge.mtu"])
		if err != nil {
			return err
		}
	}

	// Ensure correct sysctls are set on uplink veth interfaces to avoid getting IPv6 link-local addresses.
	_, err := shared.RunCommand("sysctl",
		fmt.Sprintf("net.ipv6.conf.%s.disable_ipv6=1", vars.parentEnd),
		fmt.Sprintf("net.ipv6.conf.%s.disable_ipv6=1", vars.ovsEnd),
		fmt.Sprintf("net.ipv6.conf.%s.forwarding=0", vars.parentEnd),
		fmt.Sprintf("net.ipv6.conf.%s.forwarding=0", vars.ovsEnd),
	)
	if err != nil {
		return errors.Wrapf(err, "Failed to configure uplink veth interfaces %q and %q", vars.parentEnd, vars.ovsEnd)
	}

	// Connect parent end of veth pair to parent bridge and bring up.
	_, err = shared.RunCommand("ip", "link", "set", "master", parentNet.Name(), "dev", vars.parentEnd, "up")
	if err != nil {
		return errors.Wrapf(err, "Failed to connect uplink veth interface %q to parent bridge %q", vars.parentEnd, parentNet.Name())
	}

	// Ensure uplink OVS end veth interface is up.
	_, err = shared.RunCommand("ip", "link", "set", "dev", vars.ovsEnd, "up")
	if err != nil {
		return errors.Wrapf(err, "Failed to bring up parent veth interface %q", vars.ovsEnd)
	}

	// Create parent OVS bridge if needed.
	ovs := openvswitch.NewOVS()
	err = ovs.BridgeAdd(vars.ovsBridge, true)
	if err != nil {
		return errors.Wrapf(err, "Failed to create parent uplink OVS bridge %q", vars.ovsBridge)
	}

	// Connect OVS end veth interface to OVS bridge.
	err = ovs.BridgePortAdd(vars.ovsBridge, vars.ovsEnd, true)
	if err != nil {
		return errors.Wrapf(err, "Failed to connect uplink veth interface %q to parent OVS bridge %q", vars.ovsEnd, vars.ovsBridge)
	}

	// Associate OVS bridge to logical OVN provider.
	err = ovs.OVNBridgeMappingAdd(vars.ovsBridge, parentNet.Name())
	if err != nil {
		return errors.Wrapf(err, "Failed to associate parent OVS bridge %q to OVN provider %q", vars.ovsBridge, parentNet.Name())
	}

	routerExtPortIPv6 := net.ParseIP(n.config[ovnVolatileParentIPv6])
	if routerExtPortIPv6 != nil {
		// Now that the OVN router is connected to the uplink parent bridge, attempt to ping the OVN
		// router's external IPv6 from the LXD host running the parent bridge in an attempt to trigger the
		// OVN router to learn the parent uplink gateway's MAC address. This is to work around a bug in
		// older versions of OVN that meant that the OVN router would not attempt to learn the external
		// uplink IPv6 gateway MAC address when using SNAT, meaning that external IPv6 connectivity
		// wouldn't work until the next router advertisement was sent (which could be several minutes).
		// By pinging the OVN router's external IP this will trigger an NDP request from the parent bridge
		// which will cause the OVN router to learn its MAC address.
		go func() {
			// Try several attempts as it can take a few seconds for the network to come up.
			for i := 0; i < 5; i++ {
				if pingIP(routerExtPortIPv6) {
					n.logger.Debug("OVN router external IPv6 address reachable", log.Ctx{"ip": routerExtPortIPv6.String()})
					return
				}

				time.Sleep(time.Second)
			}

			// We would expect this on a chassis node that isn't the active router gateway, it doesn't
			// always indicate a problem.
			n.logger.Debug("OVN router external IPv6 address unreachable", log.Ctx{"ip": routerExtPortIPv6.String()})
		}()
	}

	revert.Success()
	return nil
}

// deleteParentPort deletes the parent uplink connection.
func (n *ovn) deleteParentPort() error {
	// Parent network must be in default project.
	if n.config["network"] != "" {
		parentNet, err := LoadByName(n.state, project.Default, n.config["network"])
		if err != nil {
			return errors.Wrapf(err, "Failed loading parent network")
		}

		// Lock parent network so we don't race each other networks using the OVS uplink bridge.
		unlock := locking.Lock(n.parentOperationLockName(parentNet))
		defer unlock()

		switch parentNet.Type() {
		case "bridge":
			return n.deleteParentPortBridge(parentNet)
		}

		return fmt.Errorf("Failed deleting parent port, network type %q unsupported as OVN parent", parentNet.Type())
	}

	return nil
}

// deleteParentPortBridge deletes parent uplink OVS bridge, OVN bridge mappings and veth interfaces if not in use.
func (n *ovn) deleteParentPortBridge(parentNet Network) error {
	// Check OVS uplink bridge exists, if it does, check how many ports it has.
	removeVeths := false
	vars := n.parentPortBridgeVars(parentNet)
	if InterfaceExists(vars.ovsBridge) {
		ovs := openvswitch.NewOVS()
		ports, err := ovs.BridgePortList(vars.ovsBridge)
		if err != nil {
			return err
		}

		// If the OVS bridge has only 1 port (the OVS veth end) or fewer connected then we can delete it.
		if len(ports) <= 1 {
			removeVeths = true

			err = ovs.OVNBridgeMappingDelete(vars.ovsBridge, parentNet.Name())
			if err != nil {
				return err
			}

			err = ovs.BridgeDelete(vars.ovsBridge)
			if err != nil {
				return err
			}
		}
	} else {
		removeVeths = true // Remove the veths if OVS bridge already gone.
	}

	// Remove the veth interfaces if they exist.
	if removeVeths {
		if InterfaceExists(vars.parentEnd) {
			_, err := shared.RunCommand("ip", "link", "delete", "dev", vars.parentEnd)
			if err != nil {
				return errors.Wrapf(err, "Failed to delete the uplink veth interface %q", vars.parentEnd)
			}
		}

		if InterfaceExists(vars.ovsEnd) {
			_, err := shared.RunCommand("ip", "link", "delete", "dev", vars.ovsEnd)
			if err != nil {
				return errors.Wrapf(err, "Failed to delete the uplink veth interface %q", vars.ovsEnd)
			}
		}
	}

	return nil
}

// FillConfig fills requested config with any default values.
func (n *ovn) FillConfig(config map[string]string) error {
	if config["ipv4.address"] == "" {
		config["ipv4.address"] = "auto"
	}

	if config["ipv6.address"] == "" {
		content, err := ioutil.ReadFile("/proc/sys/net/ipv6/conf/default/disable_ipv6")
		if err == nil && string(content) == "0\n" {
			config["ipv6.address"] = "auto"
		}
	}

	// Now populate "auto" values where needed.
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

	return nil
}

// Create sets up network in OVN Northbound database.
func (n *ovn) Create(clientType cluster.ClientType) error {
	n.logger.Debug("Create", log.Ctx{"clientType": clientType, "config": n.config})

	// We only need to setup the OVN Northbound database once, not on every clustered node.
	if clientType == cluster.ClientTypeNormal {
		err := n.setup(false)
		if err != nil {
			return err
		}
	}

	return nil
}

// allowedUplinkNetworks returns a list of allowed networks to use as uplinks based on project restrictions.
func (n *ovn) allowedUplinkNetworks() ([]string, error) {
	// Uplink networks are always from the default project.
	networks, err := n.state.Cluster.GetNetworks(project.Default)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed getting uplink networks")
	}

	// Remove ourselves from the networks list if we are in the default project.
	if n.project == project.Default {
		allNets := networks
		networks = make([]string, 0, len(allNets)-1)
		for _, network := range allNets {
			if network == n.name {
				continue
			}

			networks = append(networks, network)
		}
	}

	// Load the project to get uplink network restrictions.
	var project *api.Project
	err = n.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		project, err = tx.GetProject(n.project)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to load restrictions for project %q", n.project)
	}

	// If project is not restricted, return full network list.
	if !shared.IsTrue(project.Config["restricted"]) {
		return networks, nil
	}

	allowedNetworks := []string{}

	// There are no allowed networks if restricted.networks.uplinks is not set.
	if project.Config["restricted.networks.uplinks"] == "" {
		return allowedNetworks, nil
	}

	// Parse the allowed uplinks and return any that are present in the actual defined networks.
	allowedRestrictedUplinks := strings.Split(project.Config["restricted.networks.uplinks"], ",")

	for _, allowedRestrictedUplink := range allowedRestrictedUplinks {
		allowedRestrictedUplink = strings.TrimSpace(allowedRestrictedUplink)

		if shared.StringInSlice(allowedRestrictedUplink, networks) {
			allowedNetworks = append(allowedNetworks, allowedRestrictedUplink)
		}
	}

	return allowedNetworks, nil
}

func (n *ovn) setup(update bool) error {
	// If we are in mock mode, just no-op.
	if n.state.OS.MockMode {
		return nil
	}

	n.logger.Debug("Setting up network")

	revert := revert.New()
	defer revert.Fail()

	client, err := n.getClient()
	if err != nil {
		return err
	}

	var routerExtPortIPv4, routerIntPortIPv4, routerExtPortIPv6, routerIntPortIPv6 net.IP
	var routerExtPortIPv4Net, routerIntPortIPv4Net, routerExtPortIPv6Net, routerIntPortIPv6Net *net.IPNet

	// Record updated config so we can store back into DB and n.config variable.
	updatedConfig := make(map[string]string)

	// Check project restrictions.
	allowedUplinkNetworks, err := n.allowedUplinkNetworks()
	if err != nil {
		return err
	}

	if n.config["network"] != "" {
		if !shared.StringInSlice(n.config["network"], allowedUplinkNetworks) {
			return fmt.Errorf(`Option "network" value %q is not one of the allowed uplink networks in project`, n.config["network"])
		}
	} else {
		allowedNetworkCount := len(allowedUplinkNetworks)
		if allowedNetworkCount == 0 {
			return fmt.Errorf(`No allowed uplink networks in project`)
		} else if allowedNetworkCount == 1 {
			// If there is only one allowed uplink network then use it if not specified by user.
			updatedConfig["network"] = allowedUplinkNetworks[0]
		} else {
			return fmt.Errorf(`Option "network" is required`)
		}
	}

	// Get bridge MTU to use.
	bridgeMTU := n.getBridgeMTU()
	if bridgeMTU == 0 {
		// If no manual bridge MTU specified, derive it from the underlay network.
		bridgeMTU, err = n.getOptimalBridgeMTU()
		if err != nil {
			return errors.Wrapf(err, "Failed getting optimal bridge MTU")
		}

		// Save to config so the value can be read by instances connecting to network.
		updatedConfig["bridge.mtu"] = fmt.Sprintf("%d", bridgeMTU)
	}

	// Apply any config dynamically generated to the current config and store back to DB in single transaction.
	if len(updatedConfig) > 0 {
		for k, v := range updatedConfig {
			n.config[k] = v
		}

		err := n.state.Cluster.Transaction(func(tx *db.ClusterTx) error {
			err = tx.UpdateNetwork(n.id, n.description, n.config)
			if err != nil {
				return errors.Wrapf(err, "Failed saving optimal bridge MTU")
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	// Get router MAC address.
	routerMAC, err := n.getRouterMAC()
	if err != nil {
		return err
	}

	// Setup parent port (do this first to check parent is suitable).
	parent, err := n.setupParentPort(routerMAC)
	if err != nil {
		return err
	}

	// Parse router IP config.
	if parent.routerExtPortIPv4Net != "" {
		routerExtPortIPv4, routerExtPortIPv4Net, err = net.ParseCIDR(parent.routerExtPortIPv4Net)
		if err != nil {
			return errors.Wrapf(err, "Failed parsing router's external parent port IPv4 Net")
		}
	}

	if parent.routerExtPortIPv6Net != "" {
		routerExtPortIPv6, routerExtPortIPv6Net, err = net.ParseCIDR(parent.routerExtPortIPv6Net)
		if err != nil {
			return errors.Wrapf(err, "Failed parsing router's external parent port IPv6 Net")
		}
	}

	if n.getRouterIntPortIPv4Net() != "" {
		routerIntPortIPv4, routerIntPortIPv4Net, err = net.ParseCIDR(n.getRouterIntPortIPv4Net())
		if err != nil {
			return errors.Wrapf(err, "Failed parsing router's internal port IPv4 Net")
		}
	}

	if n.getRouterIntPortIPv6Net() != "" {
		routerIntPortIPv6, routerIntPortIPv6Net, err = net.ParseCIDR(n.getRouterIntPortIPv6Net())
		if err != nil {
			return errors.Wrapf(err, "Failed parsing router's internal port IPv6 Net")
		}
	}

	// Create chassis group.
	err = client.ChassisGroupAdd(n.getChassisGroupName(), update)
	if err != nil {
		return err
	}

	revert.Add(func() { client.ChassisGroupDelete(n.getChassisGroupName()) })

	// Create logical router.
	if update {
		client.LogicalRouterDelete(n.getRouterName())
	}

	err = client.LogicalRouterAdd(n.getRouterName())
	if err != nil {
		return errors.Wrapf(err, "Failed adding router")
	}
	revert.Add(func() { client.LogicalRouterDelete(n.getRouterName()) })

	// Configure logical router.

	// Add default routes.
	if parent.routerExtGwIPv4 != nil {
		err = client.LogicalRouterRouteAdd(n.getRouterName(), &net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)}, parent.routerExtGwIPv4)
		if err != nil {
			return errors.Wrapf(err, "Failed adding IPv4 default route")
		}
	}

	if parent.routerExtGwIPv6 != nil {
		err = client.LogicalRouterRouteAdd(n.getRouterName(), &net.IPNet{IP: net.IPv6zero, Mask: net.CIDRMask(0, 128)}, parent.routerExtGwIPv6)
		if err != nil {
			return errors.Wrapf(err, "Failed adding IPv6 default route")
		}
	}

	// Add SNAT rules.
	if routerIntPortIPv4Net != nil && routerExtPortIPv4 != nil {
		err = client.LogicalRouterSNATAdd(n.getRouterName(), routerIntPortIPv4Net, routerExtPortIPv4)
		if err != nil {
			return err
		}
	}

	if routerIntPortIPv6Net != nil && routerExtPortIPv6 != nil {
		err = client.LogicalRouterSNATAdd(n.getRouterName(), routerIntPortIPv6Net, routerExtPortIPv6)
		if err != nil {
			return err
		}
	}

	// Generate external router port IPs (in CIDR format).
	extRouterIPs := []*net.IPNet{}
	if routerExtPortIPv4Net != nil {
		extRouterIPs = append(extRouterIPs, &net.IPNet{
			IP:   routerExtPortIPv4,
			Mask: routerExtPortIPv4Net.Mask,
		})
	}

	if routerExtPortIPv6Net != nil {
		extRouterIPs = append(extRouterIPs, &net.IPNet{
			IP:   routerExtPortIPv6,
			Mask: routerExtPortIPv6Net.Mask,
		})
	}

	if len(extRouterIPs) > 0 {
		// Create external logical switch.
		if update {
			client.LogicalSwitchDelete(n.getExtSwitchName())
		}

		err = client.LogicalSwitchAdd(n.getExtSwitchName(), false)
		if err != nil {
			return errors.Wrapf(err, "Failed adding external switch")
		}
		revert.Add(func() { client.LogicalSwitchDelete(n.getExtSwitchName()) })

		// Create external router port.
		err = client.LogicalRouterPortAdd(n.getRouterName(), n.getRouterExtPortName(), routerMAC, extRouterIPs...)
		if err != nil {
			return errors.Wrapf(err, "Failed adding external router port")
		}
		revert.Add(func() { client.LogicalRouterPortDelete(n.getRouterExtPortName()) })

		// Associate external router port to chassis group.
		err = client.LogicalRouterPortLinkChassisGroup(n.getRouterExtPortName(), n.getChassisGroupName())
		if err != nil {
			return errors.Wrapf(err, "Failed linking external router port to chassis group")
		}

		// Create external switch port and link to router port.
		err = client.LogicalSwitchPortAdd(n.getExtSwitchName(), n.getExtSwitchRouterPortName(), false)
		if err != nil {
			return errors.Wrapf(err, "Failed adding external switch router port")
		}
		revert.Add(func() { client.LogicalSwitchPortDelete(n.getExtSwitchRouterPortName()) })

		err = client.LogicalSwitchPortLinkRouter(n.getExtSwitchRouterPortName(), n.getRouterExtPortName())
		if err != nil {
			return errors.Wrapf(err, "Failed linking external router port to external switch port")
		}

		// Create external switch port and link to external provider network.
		err = client.LogicalSwitchPortAdd(n.getExtSwitchName(), n.getExtSwitchProviderPortName(), false)
		if err != nil {
			return errors.Wrapf(err, "Failed adding external switch provider port")
		}
		revert.Add(func() { client.LogicalSwitchPortDelete(n.getExtSwitchProviderPortName()) })

		err = client.LogicalSwitchPortLinkProviderNetwork(n.getExtSwitchProviderPortName(), parent.extSwitchProviderName)
		if err != nil {
			return errors.Wrapf(err, "Failed linking external switch provider port to external provider network")
		}
	}

	// Create internal logical switch if not updating.
	err = client.LogicalSwitchAdd(n.getIntSwitchName(), update)
	if err != nil {
		return errors.Wrapf(err, "Failed adding internal switch")
	}
	revert.Add(func() { client.LogicalSwitchDelete(n.getIntSwitchName()) })

	// Setup IP allocation config on logical switch.
	err = client.LogicalSwitchSetIPAllocation(n.getIntSwitchName(), &openvswitch.OVNIPAllocationOpts{
		PrefixIPv4:  routerIntPortIPv4Net,
		PrefixIPv6:  routerIntPortIPv6Net,
		ExcludeIPv4: []shared.IPRange{{Start: routerIntPortIPv4}},
	})
	if err != nil {
		return errors.Wrapf(err, "Failed setting IP allocation settings on internal switch")
	}

	var dhcpv4UUID, dhcpv6UUID string

	if update {
		// Find first existing DHCP options set for IPv4 and IPv6 and update them instead of adding sets.
		existingOpts, err := client.LogicalSwitchDHCPOptionsGet(n.getIntSwitchName())
		if err != nil {
			return errors.Wrapf(err, "Failed getting existing DHCP settings for internal switch")
		}

		for _, existingOpt := range existingOpts {
			if existingOpt.CIDR.IP.To4() == nil {
				if dhcpv6UUID == "" {
					dhcpv6UUID = existingOpt.UUID
				}
			} else {
				if dhcpv4UUID == "" {
					dhcpv4UUID = existingOpt.UUID
				}
			}
		}
	}

	// Create DHCPv4 options for internal switch.
	err = client.LogicalSwitchDHCPv4OptionsSet(n.getIntSwitchName(), dhcpv4UUID, routerIntPortIPv4Net, &openvswitch.OVNDHCPv4Opts{
		ServerID:           routerIntPortIPv4,
		ServerMAC:          routerMAC,
		Router:             routerIntPortIPv4,
		RecursiveDNSServer: parent.dnsIPv4,
		DomainName:         n.getDomainName(),
		LeaseTime:          time.Duration(time.Hour * 1),
		MTU:                bridgeMTU,
	})
	if err != nil {
		return errors.Wrapf(err, "Failed adding DHCPv4 settings for internal switch")
	}

	// Create DHCPv6 options for internal switch.
	err = client.LogicalSwitchDHCPv6OptionsSet(n.getIntSwitchName(), dhcpv6UUID, routerIntPortIPv6Net, &openvswitch.OVNDHCPv6Opts{
		ServerID:           routerMAC,
		RecursiveDNSServer: parent.dnsIPv6,
		DNSSearchList:      n.getDNSSearchList(),
	})
	if err != nil {
		return errors.Wrapf(err, "Failed adding DHCPv6 settings for internal switch")
	}

	// Generate internal router port IPs (in CIDR format).
	intRouterIPs := []*net.IPNet{}
	if routerIntPortIPv4Net != nil {
		intRouterIPs = append(intRouterIPs, &net.IPNet{
			IP:   routerIntPortIPv4,
			Mask: routerIntPortIPv4Net.Mask,
		})
	}

	if routerIntPortIPv6Net != nil {
		intRouterIPs = append(intRouterIPs, &net.IPNet{
			IP:   routerIntPortIPv6,
			Mask: routerIntPortIPv6Net.Mask,
		})
	}

	// Create internal router port.
	err = client.LogicalRouterPortAdd(n.getRouterName(), n.getRouterIntPortName(), routerMAC, intRouterIPs...)
	if err != nil {
		return errors.Wrapf(err, "Failed adding internal router port")
	}
	revert.Add(func() { client.LogicalRouterPortDelete(n.getRouterIntPortName()) })

	// Set IPv6 router advertisement settings.
	if routerIntPortIPv6Net != nil {
		adressMode := openvswitch.OVNIPv6AddressModeSLAAC
		if shared.IsTrue(n.config["ipv6.dhcp.stateful"]) {
			adressMode = openvswitch.OVNIPv6AddressModeDHCPStateful
		}

		var recursiveDNSServer net.IP
		if len(parent.dnsIPv6) > 0 {
			recursiveDNSServer = parent.dnsIPv6[0] // OVN only supports 1 RA DNS server.
		}

		err = client.LogicalRouterPortSetIPv6Advertisements(n.getRouterIntPortName(), &openvswitch.OVNIPv6RAOpts{
			AddressMode:        adressMode,
			SendPeriodic:       true,
			DNSSearchList:      n.getDNSSearchList(),
			RecursiveDNSServer: recursiveDNSServer,
			MTU:                bridgeMTU,

			// Keep these low until we support DNS search domains via DHCPv4, as otherwise RA DNSSL
			// won't take effect until advert after DHCPv4 has run on instance.
			MinInterval: time.Duration(time.Second * 30),
			MaxInterval: time.Duration(time.Minute * 1),
		})
		if err != nil {
			return errors.Wrapf(err, "Failed setting internal router port IPv6 advertisement settings")
		}
	}

	// Create internal switch port and link to router port.
	err = client.LogicalSwitchPortAdd(n.getIntSwitchName(), n.getIntSwitchRouterPortName(), update)
	if err != nil {
		return errors.Wrapf(err, "Failed adding internal switch router port")
	}
	revert.Add(func() { client.LogicalSwitchPortDelete(n.getIntSwitchRouterPortName()) })

	err = client.LogicalSwitchPortLinkRouter(n.getIntSwitchRouterPortName(), n.getRouterIntPortName())
	if err != nil {
		return errors.Wrapf(err, "Failed linking internal router port to internal switch port")
	}

	revert.Success()
	return nil
}

// addChassisGroupEntry adds an entry for the local OVS chassis to the OVN logical network's chassis group.
func (n *ovn) addChassisGroupEntry() error {
	client, err := n.getClient()
	if err != nil {
		return err
	}

	// Add local chassis to chassis group.
	ovs := openvswitch.NewOVS()
	chassisID, err := ovs.ChassisID()
	if err != nil {
		return errors.Wrapf(err, "Failed getting OVS Chassis ID")
	}

	var priority uint = ovnChassisPriorityMax
	err = client.ChassisGroupChassisAdd(n.getChassisGroupName(), chassisID, priority)
	if err != nil {
		return errors.Wrapf(err, "Failed adding OVS chassis %q with priority %d to chassis group %q", chassisID, priority, n.getChassisGroupName())
	}

	return nil
}

// deleteChassisGroupEntry deletes an entry for the local OVS chassis from the OVN logical network's chassis group.
func (n *ovn) deleteChassisGroupEntry() error {
	client, err := n.getClient()
	if err != nil {
		return err
	}

	// Add local chassis to chassis group.
	ovs := openvswitch.NewOVS()
	chassisID, err := ovs.ChassisID()
	if err != nil {
		return errors.Wrapf(err, "Failed getting OVS Chassis ID")
	}

	err = client.ChassisGroupChassisDelete(n.getChassisGroupName(), chassisID)
	if err != nil {
		return errors.Wrapf(err, "Failed deleting OVS chassis %q from chassis group %q", chassisID, n.getChassisGroupName())
	}

	return nil
}

// Delete deletes a network.
func (n *ovn) Delete(clientType cluster.ClientType) error {
	n.logger.Debug("Delete", log.Ctx{"clientType": clientType})

	err := n.Stop()
	if err != nil {
		return err
	}

	if clientType == cluster.ClientTypeNormal {
		client, err := n.getClient()
		if err != nil {
			return err
		}

		err = client.LogicalRouterDelete(n.getRouterName())
		if err != nil {
			return err
		}

		err = client.LogicalSwitchDelete(n.getExtSwitchName())
		if err != nil {
			return err
		}

		err = client.LogicalSwitchDelete(n.getIntSwitchName())
		if err != nil {
			return err
		}

		err = client.LogicalRouterPortDelete(n.getRouterExtPortName())
		if err != nil {
			return err
		}

		err = client.LogicalRouterPortDelete(n.getRouterIntPortName())
		if err != nil {
			return err
		}

		err = client.LogicalSwitchPortDelete(n.getExtSwitchRouterPortName())
		if err != nil {
			return err
		}

		err = client.LogicalSwitchPortDelete(n.getExtSwitchProviderPortName())
		if err != nil {
			return err
		}

		err = client.LogicalSwitchPortDelete(n.getIntSwitchRouterPortName())
		if err != nil {
			return err
		}

		// Must be done after logical router removal.
		err = client.ChassisGroupDelete(n.getChassisGroupName())
		if err != nil {
			return err
		}
	}

	return n.common.delete(clientType)
}

// Rename renames a network.
func (n *ovn) Rename(newName string) error {
	n.logger.Debug("Rename", log.Ctx{"newName": newName})

	// Rename common steps.
	err := n.common.rename(newName)
	if err != nil {
		return err
	}

	return nil
}

// Start starts adds the local OVS chassis ID to the OVN chass group and starts the local OVS parent uplink port.
func (n *ovn) Start() error {
	n.logger.Debug("Start")

	if n.status == api.NetworkStatusPending {
		return fmt.Errorf("Cannot start pending network")
	}

	// Add local node's OVS chassis ID to logical chassis group.
	err := n.addChassisGroupEntry()
	if err != nil {
		return err
	}

	err = n.startParentPort()
	if err != nil {
		return err
	}

	return nil
}

// Stop deletes the local OVS parent uplink port (if unused) and deletes the local OVS chassis ID from the
// OVN chass group
func (n *ovn) Stop() error {
	n.logger.Debug("Stop")

	// Delete local OVS chassis ID from logical OVN HA chassis group.
	err := n.deleteChassisGroupEntry()
	if err != nil {
		return err
	}

	time.Sleep(2 * time.Second) // Give some time for the chassis deletion to tear down patch ports.

	// Delete local parent uplink port.
	// This must occur after the local OVS chassis ID is removed from the OVN HA chassis group so that the
	// OVN patch port connection is removed for this network and we can correctly detect whether there are
	// any other OVN networks using this uplink bridge before removing it.
	err = n.deleteParentPort()
	if err != nil {
		return err
	}

	return nil
}

// Update updates the network. Accepts notification boolean indicating if this update request is coming from a
// cluster notification, in which case do not update the database, just apply local changes needed.
func (n *ovn) Update(newNetwork api.NetworkPut, targetNode string, clientType cluster.ClientType) error {
	n.logger.Debug("Update", log.Ctx{"clientType": clientType, "newNetwork": newNetwork})

	// Populate default values if they are missing.
	err := n.FillConfig(newNetwork.Config)
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
		n.common.update(oldNetwork, targetNode, clientType)

		// Reset any change that was made to logical network.
		if clientType == cluster.ClientTypeNormal {
			n.setup(true)
		}
	})

	// Apply changes to database.
	err = n.common.update(newNetwork, targetNode, clientType)
	if err != nil {
		return err
	}

	// Re-setup the logical network if needed.
	if len(changedKeys) > 0 && clientType == cluster.ClientTypeNormal {
		err = n.setup(true)
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// getInstanceDevicePortName returns the switch port name to use for an instance device.
func (n *ovn) getInstanceDevicePortName(instanceID int, deviceName string) openvswitch.OVNSwitchPort {
	return openvswitch.OVNSwitchPort(fmt.Sprintf("%s-%d-%s", n.getIntSwitchInstancePortPrefix(), instanceID, deviceName))
}

// instanceDevicePortAdd adds an instance device port to the internal logical switch and returns the port name.
func (n *ovn) instanceDevicePortAdd(instanceID int, instanceName string, deviceName string, mac net.HardwareAddr, ips []net.IP) (openvswitch.OVNSwitchPort, error) {
	var dhcpV4ID, dhcpv6ID string

	revert := revert.New()
	defer revert.Fail()

	client, err := n.getClient()
	if err != nil {
		return "", err
	}

	// Get DHCP options IDs.
	if n.getRouterIntPortIPv4Net() != "" {
		_, routerIntPortIPv4Net, err := net.ParseCIDR(n.getRouterIntPortIPv4Net())
		if err != nil {
			return "", err
		}

		dhcpV4ID, err = client.LogicalSwitchDHCPOptionsGetID(n.getIntSwitchName(), routerIntPortIPv4Net)
		if err != nil {
			return "", err
		}
	}

	if n.getRouterIntPortIPv6Net() != "" {
		_, routerIntPortIPv6Net, err := net.ParseCIDR(n.getRouterIntPortIPv6Net())
		if err != nil {
			return "", err
		}

		dhcpv6ID, err = client.LogicalSwitchDHCPOptionsGetID(n.getIntSwitchName(), routerIntPortIPv6Net)
		if err != nil {
			return "", err
		}
	}

	instancePortName := n.getInstanceDevicePortName(instanceID, deviceName)

	// Add port with mayExist set to true, so that if instance port exists, we don't fail and continue below
	// to configure the port as needed. This is required in case the OVN northbound database was unavailable
	// when the instance NIC was stopped and was unable to remove the port on last stop, which would otherwise
	// prevent future NIC starts.
	err = client.LogicalSwitchPortAdd(n.getIntSwitchName(), instancePortName, true)
	if err != nil {
		return "", err
	}

	revert.Add(func() { client.LogicalSwitchPortDelete(instancePortName) })

	err = client.LogicalSwitchPortSet(instancePortName, &openvswitch.OVNSwitchPortOpts{
		DHCPv4OptsID: dhcpV4ID,
		DHCPv6OptsID: dhcpv6ID,
		MAC:          mac,
		IPs:          ips,
	})
	if err != nil {
		return "", err
	}

	err = client.LogicalSwitchPortSetDNS(n.getIntSwitchName(), instancePortName, fmt.Sprintf("%s.%s", instanceName, n.getDomainName()))
	if err != nil {
		return "", err
	}

	revert.Success()
	return instancePortName, nil
}

// instanceDevicePortIPs returns the dynamically allocated IPs for a device port.
func (n *ovn) instanceDevicePortDynamicIPs(instanceID int, deviceName string) ([]net.IP, error) {
	instancePortName := n.getInstanceDevicePortName(instanceID, deviceName)

	client, err := n.getClient()
	if err != nil {
		return nil, err
	}

	return client.LogicalSwitchPortDynamicIPs(instancePortName)
}

// instanceDevicePortDelete deletes an instance device port from the internal logical switch.
func (n *ovn) instanceDevicePortDelete(instanceID int, deviceName string) error {
	instancePortName := n.getInstanceDevicePortName(instanceID, deviceName)

	client, err := n.getClient()
	if err != nil {
		return err
	}

	err = client.LogicalSwitchPortDelete(instancePortName)
	if err != nil {
		return err
	}

	err = client.LogicalSwitchPortDeleteDNS(n.getIntSwitchName(), instancePortName)
	if err != nil {
		return err
	}

	return nil
}

// DHCPv4Subnet returns the DHCPv4 subnet (if DHCP is enabled on network).
func (n *ovn) DHCPv4Subnet() *net.IPNet {
	// DHCP is disabled on this network (an empty ipv4.dhcp setting indicates enabled by default).
	if n.config["ipv4.dhcp"] != "" && !shared.IsTrue(n.config["ipv4.dhcp"]) {
		return nil
	}

	_, subnet, err := net.ParseCIDR(n.config["ipv4.address"])
	if err != nil {
		return nil
	}

	return subnet
}

// DHCPv6Subnet returns the DHCPv6 subnet (if DHCP or SLAAC is enabled on network).
func (n *ovn) DHCPv6Subnet() *net.IPNet {
	// DHCP is disabled on this network (an empty ipv6.dhcp setting indicates enabled by default).
	if n.config["ipv6.dhcp"] != "" && !shared.IsTrue(n.config["ipv6.dhcp"]) {
		return nil
	}

	_, subnet, err := net.ParseCIDR(n.config["ipv6.address"])
	if err != nil {
		return nil
	}

	return subnet
}
