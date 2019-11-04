package firewall

import (
	"fmt"
	"net"
	"strings"

	"github.com/lxc/lxd/lxd/iptables"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/shared"
)

// XTables is an implmentation of LXD firewall using {ip, ip6, eb}tables
type XTables struct {}

// Lower-level clear functions

// NetworkClear removes network rules.
func (xt *XTables) NetworkClear(protocol string, comment string, table string) error {
	return iptables.NetworkClear(protocol, comment, table)
}

// ContainerClear removes container rules.
func (xt *XTables) ContainerClear(protocol string, comment string, table string) error {
	return iptables.ContainerClear(protocol, comment, table)
}

// Proxy
func (xt *XTables) ProxySetupNAT(ipv string, IPAddr string, comment string, connType, address, port string, cPort string) error {
	if IPAddr != "" {
		err := iptables.ContainerPrepend(ipv, comment, "nat", "PREROUTING", "-p", connType, "--destination", address, "--dport", port, "-j", "DNAT", "--to-destination", fmt.Sprintf("%s:%s", IPAddr, cPort))
		if err != nil {
			return err
		}

		err = iptables.ContainerPrepend(ipv, comment, "nat", "OUTPUT", "-p", connType, "--destination", address, "--dport", port, "-j", "DNAT", "--to-destination", fmt.Sprintf("%s:%s", IPAddr, cPort))
		if err != nil {
			return err
		}
	}

	return nil
}

// NIC bridged
func (xt *XTables) BridgeRemoveFilters(m deviceConfig.Device, IPv4 net.IP, IPv6 net.IP) error {
	// Get a current list of rules active on the host.
	out, err := shared.RunCommand("ebtables", "--concurrent", "-L", "--Lmac2", "--Lx")
	if err != nil {
		return fmt.Errorf("Failed to remove network filters for %s: %v", m["name"], err)
	}

	// Get a list of rules that we would have applied on instance start.
	rules := generateFilterEbtablesRules(m, IPv4, IPv6)

	errs := []error{}
	// Iterate through each active rule on the host and try and match it to one the LXD rules.
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		fieldsLen := len(fields)

		for _, rule := range rules {
			// Rule doesn't match if the field lenths aren't the same, move on.
			if len(rule) != fieldsLen {
				continue
			}

			// Check whether active rule matches one of our rules to delete.
			if !matchEbtablesRule(fields, rule, true) {
				continue
			}

			// If we get this far, then the current host rule matches one of our LXD
			// rules, so we should run the modified command to delete it.
			_, err = shared.RunCommand(fields[0], append([]string{"--concurrent"}, fields[1:]...)...)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("Failed to remove network filters rule for %s: %v", m["name"], errs)
	}

	return nil
}
func (xt *XTables) BridgeSetFilters(m deviceConfig.Device) error {
	return nil
}

// Network
func (xt *XTables) NetworkSetup(oldConfig map[string]string) error {
	return nil
}

// generateFilterEbtablesRules returns a customised set of ebtables filter rules based on the device.
func generateFilterEbtablesRules(m deviceConfig.Device, IPv4 net.IP, IPv6 net.IP) [][]string {
	// MAC source filtering rules. Blocks any packet coming from instance with an incorrect Ethernet source MAC.
	// This is required for IP filtering too.
	rules := [][]string{
		{"ebtables", "-t", "filter", "-A", "INPUT", "-s", "!", m["hwaddr"], "-i", m["host_name"], "-j", "DROP"},
		{"ebtables", "-t", "filter", "-A", "FORWARD", "-s", "!", m["hwaddr"], "-i", m["host_name"], "-j", "DROP"},
	}

	if shared.IsTrue(m["security.ipv4_filtering"]) && IPv4 != nil {
		rules = append(rules,
			// Prevent ARP MAC spoofing (prevents the instance poisoning the ARP cache of its neighbours with a MAC address that isn't its own).
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "ARP", "-i", m["host_name"], "--arp-mac-src", "!", m["hwaddr"], "-j", "DROP"},
			[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "ARP", "-i", m["host_name"], "--arp-mac-src", "!", m["hwaddr"], "-j", "DROP"},
			// Prevent ARP IP spoofing (prevents the instance redirecting traffic for IPs that are not its own).
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "ARP", "-i", m["host_name"], "--arp-ip-src", "!", IPv4.String(), "-j", "DROP"},
			[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "ARP", "-i", m["host_name"], "--arp-ip-src", "!", IPv4.String(), "-j", "DROP"},
			// Allow DHCPv4 to the host only. This must come before the IP source filtering rules below.
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv4", "-s", m["hwaddr"], "-i", m["host_name"], "--ip-src", "0.0.0.0", "--ip-dst", "255.255.255.255", "--ip-proto", "udp", "--ip-dport", "67", "-j", "ACCEPT"},
			// IP source filtering rules. Blocks any packet coming from instance with an incorrect IP source address.
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv4", "-i", m["host_name"], "--ip-src", "!", IPv4.String(), "-j", "DROP"},
			[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "IPv4", "-i", m["host_name"], "--ip-src", "!", IPv4.String(), "-j", "DROP"},
		)
	}

	if shared.IsTrue(m["security.ipv6_filtering"]) && IPv6 != nil {
		rules = append(rules,
			// Allow DHCPv6 and Router Solicitation to the host only. This must come before the IP source filtering rules below.
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv6", "-s", m["hwaddr"], "-i", m["host_name"], "--ip6-src", "fe80::/ffc0::", "--ip6-dst", "ff02::1:2/ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "--ip6-proto", "udp", "--ip6-dport", "547", "-j", "ACCEPT"},
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv6", "-s", m["hwaddr"], "-i", m["host_name"], "--ip6-src", "fe80::/ffc0::", "--ip6-dst", "ff02::2/ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "--ip6-proto", "ipv6-icmp", "--ip6-icmp-type", "router-solicitation", "-j", "ACCEPT"},
			// IP source filtering rules. Blocks any packet coming from instance with an incorrect IP source address.
			[]string{"ebtables", "-t", "filter", "-A", "INPUT", "-p", "IPv6", "-i", m["host_name"], "--ip6-src", "!", fmt.Sprintf("%s/ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", IPv6.String()), "-j", "DROP"},
			[]string{"ebtables", "-t", "filter", "-A", "FORWARD", "-p", "IPv6", "-i", m["host_name"], "--ip6-src", "!", fmt.Sprintf("%s/ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", IPv6.String()), "-j", "DROP"},
		)
	}

	return rules
}

// matchEbtablesRule compares an active rule to a supplied match rule to see if they match.
// If deleteMode is true then the "-A" flag in the active rule will be modified to "-D" and will
// not be part of the equality match. This allows delete commands to be generated from dumped add commands.
func matchEbtablesRule(activeRule []string, matchRule []string, deleteMode bool) bool {
	for i := range matchRule {
		// Active rules will be dumped in "add" format, we need to detect
		// this and switch it to "delete" mode if requested. If this has already been
		// done then move on, as we don't want to break the comparison below.
		if deleteMode && (activeRule[i] == "-A" || activeRule[i] == "-D") {
			activeRule[i] = "-D"
			continue
		}

		// Check the match rule field matches the active rule field.
		// If they don't match, then this isn't one of our rules.
		if activeRule[i] != matchRule[i] {
			return false
		}
	}

	return true
}
