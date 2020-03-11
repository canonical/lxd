package drivers

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"text/template"

	"github.com/pkg/errors"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

const nftablesNamespace = "lxd"
const nftablesContentTemplate = "nftablesContent"

// nftablesChainSeparator The "." character is specifically chosen here so as to prevent the ability for collisions
// between project prefix (which is empty if project is default) and device name combinations that both are allowed
// to contain underscores (where as instance name is not).
const nftablesChainSeparator = "."

// nftablesMinVersion We need at least 0.9.1 as this was when the arp ether saddr filters were added.
const nftablesMinVersion = "0.9.1"

// Nftables is an implmentation of LXD firewall using nftables.
type Nftables struct{}

// String returns the driver name.
func (d Nftables) String() string {
	return "nftables"
}

// Compat returns whether the host is compatible with this driver and whether the driver backend is in use.
func (d Nftables) Compat() (bool, bool) {
	// Check if nftables nft command exists, if not use xtables.
	_, err := exec.LookPath("nft")
	if err != nil {
		return false, false
	}

	// Get nftables version.
	nftVersion, err := d.hostVersion()
	if err != nil {
		logger.Debugf("Firewall nftables failed detecting nft version: %v", err)
		return false, false
	}

	// Check nft version meets minimum required.
	minVer, _ := version.NewDottedVersion(nftablesMinVersion)
	if nftVersion.Compare(minVer) < 0 {
		logger.Debugf("Firewall nftables detected nft version %q is too low, need %q or above", nftVersion, nftablesMinVersion)
		return false, false
	}

	// Check whether in use by parsing ruleset and looking for existing rules.
	ruleset, err := d.nftParseRuleset()
	if err != nil {
		logger.Errorf("Firewall nftables unable to parse existing ruleset: %v", err)
		return true, false
	}

	for _, item := range ruleset {
		if item.Type == "rule" {
			return true, true // At least one rule found indicates in use.
		}
	}

	return true, false
}

// nftGenericItem represents some common fields amongst the different nftables types.
type nftGenericItem struct {
	Type   string // Type of item (table, chain or rule).
	Family string `json:"family"` // Family of item (ip, ip6, bridge etc).
	Table  string `json:"table"`  // Table the item belongs to (for chains and rules).
	Chain  string `json:"chain"`  // Chain the item belongs to (for rules).
	Name   string `json:"name"`   // Name of item (for tables and chains).
}

// nftParseRuleset parses the ruleset and returns the generic parts as a slice of items.
func (d Nftables) nftParseRuleset() ([]nftGenericItem, error) {
	// Dump ruleset as JSON. Use -nn flags to avoid doing DNS lookups of IPs mentioned in any rules.
	cmd := exec.Command("nft", "list", "ruleset", "--json", "-nn")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	err = cmd.Start()
	if err != nil {
		return nil, err
	}
	defer cmd.Wait()

	// This only extracts certain generic parts of the ruleset, see man libnftables-json for more info.
	v := &struct {
		Nftables []map[string]nftGenericItem `json:"nftables"`
	}{}

	err = json.NewDecoder(stdout).Decode(v)
	if err != nil {
		return nil, err
	}

	items := []nftGenericItem{}
	for _, item := range v.Nftables {
		if rule, found := item["rule"]; found {
			rule.Type = "rule"
			items = append(items, rule)
		} else if chain, found := item["chain"]; found {
			chain.Type = "chain"
			items = append(items, chain)
		} else if table, found := item["table"]; found {
			table.Type = "table"
			items = append(items, table)
		}
	}

	return items, nil
}

// GetVersion returns the version of dnsmasq.
func (d Nftables) hostVersion() (*version.DottedVersion, error) {
	output, err := shared.RunCommandCLocale("nft", "--version")
	if err != nil {
		return nil, errors.Wrap(err, "Failed to check nftables version")
	}

	lines := strings.Split(string(output), " ")
	return version.NewDottedVersion(strings.TrimPrefix(lines[1], "v"))
}

// NetworkSetupForwardingPolicy allows forwarding dependent on boolean argument
func (d Nftables) NetworkSetupForwardingPolicy(networkName string, ipVersion uint, allow bool) error {
	action := "reject"
	if allow {
		action = "accept"
	}

	family, err := d.getIPFamily(ipVersion)
	if err != nil {
		return err
	}

	tplFields := map[string]interface{}{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"networkName":    networkName,
		"family":         family,
		"action":         action,
	}

	err = d.applyNftConfig(nftablesNetForwardingPolicy, tplFields)
	if err != nil {
		return errors.Wrapf(err, "Failed adding forwarding policy rules for network %q (%s)", networkName, tplFields["family"])
	}

	return nil
}

// NetworkSetupOutboundNAT configures outbound NAT.
// If srcIP is non-nil then SNAT is used with the specified address, otherwise MASQUERADE mode is used.
// Append mode is always on and so the append argument is ignored.
func (d Nftables) NetworkSetupOutboundNAT(networkName string, subnet *net.IPNet, srcIP net.IP, _ bool) error {
	family := "ip"
	if subnet.IP.To4() == nil {
		family = "ip6"
	}

	// If SNAT IP not supplied then use the IP of the outbound interface (MASQUERADE).
	srcIPStr := ""
	if srcIP != nil {
		srcIPStr = srcIP.String()
	}

	tplFields := map[string]interface{}{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"networkName":    networkName,
		"family":         family,
		"subnet":         subnet.String(),
		"srcIP":          srcIPStr,
	}

	err := d.applyNftConfig(nftablesNetOutboundNAT, tplFields)
	if err != nil {
		return errors.Wrapf(err, "Failed adding outbound NAT rules for network %q (%s)", networkName, tplFields["family"])
	}

	return nil
}

// NetworkSetupDHCPDNSAccess sets up basic nftables overrides for DHCP/DNS.
func (d Nftables) NetworkSetupDHCPDNSAccess(networkName string, ipVersion uint) error {
	family, err := d.getIPFamily(ipVersion)
	if err != nil {
		return err
	}

	tplFields := map[string]interface{}{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"networkName":    networkName,
		"family":         family,
	}

	err = d.applyNftConfig(nftablesNetDHCPDNS, tplFields)
	if err != nil {
		return errors.Wrapf(err, "Failed adding DHCP/DNS access rules for network %q (%s)", networkName, tplFields["family"])
	}

	return nil
}

// NetworkSetupDHCPv4Checksum attempts a workaround for broken DHCP clients. No-op as not supported by nftables.
// See https://wiki.nftables.org/wiki-nftables/index.php/Supported_features_compared_to_xtables#CHECKSUM.
func (d Nftables) NetworkSetupDHCPv4Checksum(networkName string) error {
	return nil
}

// NetworkClear removes the LXD network related chains.
func (d Nftables) NetworkClear(networkName string, ipVersion uint) error {
	family, err := d.getIPFamily(ipVersion)
	if err != nil {
		return err
	}

	// Remove chains created by network rules.
	err = d.removeChains([]string{family}, networkName, "fwd", "pstrt", "in", "out")
	if err != nil {
		return errors.Wrapf(err, "Failed clearing nftables rules for network %q", networkName)
	}

	return nil
}

//instanceDeviceLabel returns the unique label used for instance device chains.
func (d Nftables) instanceDeviceLabel(projectName, instanceName, deviceName string) string {
	return fmt.Sprintf("%s%s%s", project.Instance(projectName, instanceName), nftablesChainSeparator, deviceName)
}

// InstanceSetupBridgeFilter sets up the filter rules to apply bridged device IP filtering.
func (d Nftables) InstanceSetupBridgeFilter(projectName, instanceName, deviceName, parentName, hostName, hwAddr string, IPv4, IPv6 net.IP) error {
	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)

	mac, err := net.ParseMAC(hwAddr)
	if err != nil {
		return err
	}

	tplFields := map[string]interface{}{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"family":         "bridge",
		"deviceLabel":    deviceLabel,
		"parentName":     parentName,
		"hostName":       hostName,
		"hwAddr":         hwAddr,
		"hwAddrHex":      fmt.Sprintf("0x%s", hex.EncodeToString(mac)),
	}

	if IPv4 != nil {
		tplFields["ipv4Addr"] = IPv4.String()
	}

	if IPv6 != nil {
		tplFields["ipv6Addr"] = IPv6.String()
		tplFields["ipv6AddrHex"] = fmt.Sprintf("0x%s", hex.EncodeToString(IPv6))
	}

	err = d.applyNftConfig(nftablesInstanceBridgeFilter, tplFields)
	if err != nil {
		return errors.Wrapf(err, "Failed adding bridge filter rules for instance device %q (%s)", deviceLabel, tplFields["family"])
	}

	return nil
}

// InstanceClearBridgeFilter removes any filter rules that were added to apply bridged device IP filtering.
func (d Nftables) InstanceClearBridgeFilter(projectName, instanceName, deviceName, parentName, hostName, hwAddr string, IPv4, IPv6 net.IP) error {
	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)

	// Remove chains created by bridge filter rules.
	err := d.removeChains([]string{"bridge"}, deviceLabel, "in", "fwd")
	if err != nil {
		return errors.Wrapf(err, "Failed clearing bridge filter rules for instance device %q", deviceLabel)
	}

	return nil
}

// InstanceSetupProxyNAT creates DNAT rules for proxy devices.
func (d Nftables) InstanceSetupProxyNAT(projectName, instanceName, deviceName string, listen, connect *deviceConfig.ProxyAddress) error {
	connectAddrCount := len(connect.Addr)
	if connectAddrCount < 1 {
		return fmt.Errorf("At least 1 connect address must be supplied")
	}

	if len(listen.Addr) < 1 {
		return fmt.Errorf("At least 1 listen address must be supplied")
	}

	if connectAddrCount > 1 && len(listen.Addr) != connectAddrCount {
		return fmt.Errorf("More than 1 connect addresses have been supplied, but insufficient for listen addresses")
	}

	// Generate a slice of rules to add.
	var rules []map[string]interface{}
	for i, lAddr := range listen.Addr {
		listenHost, listenPort, err := net.SplitHostPort(lAddr)
		if err != nil {
			return err
		}

		// Use the connect address that corresponds to the listen address (unless only 1 is specified).
		connectIndex := 0
		if connectAddrCount > 1 {
			connectIndex = i
		}

		connectHost, connectPort, err := net.SplitHostPort(connect.Addr[connectIndex])
		if err != nil {
			return err
		}

		// Figure out which IP family we are using and format the destination host/port as appropriate.
		family := "ip"
		toDest := fmt.Sprintf("%s:%s", connectHost, connectPort)
		connectIP := net.ParseIP(connectHost)
		if connectIP.To4() == nil {
			family = "ip6"
			toDest = fmt.Sprintf("[%s]:%s", connectHost, connectPort)
		}

		rules = append(rules, map[string]interface{}{
			"family":     family,
			"connType":   listen.ConnType,
			"listenHost": listenHost,
			"listenPort": listenPort,
			"toDest":     toDest,
		})
	}

	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)
	tplFields := map[string]interface{}{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"family":         rules[0]["family"], // Family should be same for all rules, so use 1st as global.
		"deviceLabel":    deviceLabel,
		"rules":          rules,
	}

	err := d.applyNftConfig(nftablesNetProxyNAT, tplFields)
	if err != nil {
		return errors.Wrapf(err, "Failed adding proxy rules for instance device %q", deviceLabel)
	}

	return nil
}

// InstanceClearProxyNAT remove DNAT rules for proxy devices.
func (d Nftables) InstanceClearProxyNAT(projectName, instanceName, deviceName string) error {
	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)
	err := d.removeChains([]string{"ip", "ip6"}, deviceLabel, "out", "prert")
	if err != nil {
		return errors.Wrapf(err, "Failed clearing proxy rules for instance device %q", deviceLabel)
	}

	return nil
}

// getIPFamily converts IP version number into family name used by nftables.
func (d Nftables) getIPFamily(ipVersion uint) (string, error) {
	if ipVersion == 4 {
		return "ip", nil
	} else if ipVersion == 6 {
		return "ip6", nil
	}

	return "", fmt.Errorf("Invalid IP version")
}

// applyNftConfig loads the specified config template and then applies it to the common template before sending to
// the nft command to be atomically applied to the system.
func (d Nftables) applyNftConfig(tpl *template.Template, tplFields map[string]interface{}) error {
	// Load the specified template into the common template's parse tree under the nftableContentTemplate
	// name so that the nftableContentTemplate template can use it with the generic name.
	_, err := nftablesCommonTable.AddParseTree(nftablesContentTemplate, tpl.Tree)
	if err != nil {
		return errors.Wrapf(err, "Failed loading %q template", tpl.Name())
	}

	config := &strings.Builder{}
	err = nftablesCommonTable.Execute(config, tplFields)
	if err != nil {
		return errors.Wrapf(err, "Failed running %q template", tpl.Name())
	}

	_, err = shared.RunCommand("nft", config.String())
	if err != nil {
		return errors.Wrapf(err, "Failed apply nftables config")
	}

	return nil
}

// removeChains removes the specified chains from the specified families.
// If not empty, chain suffix is appended to each chain name, separated with "_".
func (d Nftables) removeChains(families []string, chainSuffix string, chains ...string) error {
	ruleset, err := d.nftParseRuleset()
	if err != nil {
		return err
	}

	fullChains := chains
	if chainSuffix != "" {
		fullChains = make([]string, 0, len(chains))
		for _, chain := range chains {
			fullChains = append(fullChains, fmt.Sprintf("%s%s%s", chain, nftablesChainSeparator, chainSuffix))
		}
	}

	for _, family := range families {
		for _, item := range ruleset {
			if item.Type == "chain" && item.Family == family && item.Table == nftablesNamespace && shared.StringInSlice(item.Name, fullChains) {
				_, err = shared.RunCommand("nft", "delete", "chain", family, nftablesNamespace, item.Name)
				if err != nil {
					return errors.Wrapf(err, "Failed deleting nftables chain %q (%s)", item.Name, family)
				}
			}
		}
	}

	return nil
}
