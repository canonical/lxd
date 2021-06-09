package drivers

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"text/template"

	"github.com/pborman/uuid"
	"github.com/pkg/errors"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/validate"
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

// Compat returns whether the driver backend is in use, and any host compatibility errors.
func (d Nftables) Compat() (bool, error) {
	// Get the kernel version.
	uname, err := shared.Uname()
	if err != nil {
		return false, err
	}

	// We require a >= 5.2 kernel to avoid weird conflicts with xtables and support for inet table NAT rules.
	releaseLen := len(uname.Release)
	if releaseLen > 1 {
		verErr := fmt.Errorf("Kernel version does not meet minimum requirement of 5.2")
		releaseParts := strings.SplitN(uname.Release, ".", 3)
		if len(releaseParts) < 2 {
			return false, errors.Wrapf(err, "Failed parsing kernel version number into parts")
		}

		majorVer := releaseParts[0]
		majorVerInt, err := strconv.Atoi(majorVer)
		if err != nil {
			return false, errors.Wrapf(err, "Failed parsing kernel major version number %q", majorVer)
		}

		if majorVerInt < 5 {
			return false, verErr
		}

		if majorVerInt == 5 {
			minorVer := releaseParts[1]
			minorVerInt, err := strconv.Atoi(minorVer)
			if err != nil {
				return false, errors.Wrapf(err, "Failed parsing kernel minor version number %q", minorVer)
			}

			if minorVerInt < 2 {
				return false, verErr
			}
		}
	}

	// Check if nftables nft command exists, if not use xtables.
	_, err = exec.LookPath("nft")
	if err != nil {
		return false, fmt.Errorf("Backend command %q missing", "nft")
	}

	// Get nftables version.
	nftVersion, err := d.hostVersion()
	if err != nil {
		return false, errors.Wrapf(err, "Failed detecting nft version")
	}

	// Check nft version meets minimum required.
	minVer, _ := version.NewDottedVersion(nftablesMinVersion)
	if nftVersion.Compare(minVer) < 0 {
		return false, fmt.Errorf("nft version %q is too low, need %q or above", nftVersion, nftablesMinVersion)
	}

	// Check that nftables works at all (some kernels let you list ruleset despite missing support).
	testTable := fmt.Sprintf("lxd_test_%s", uuid.New())

	_, err = shared.RunCommandCLocale("nft", "create", "table", testTable)
	if err != nil {
		return false, errors.Wrapf(err, "Failed to create a test table")
	}

	_, err = shared.RunCommandCLocale("nft", "delete", "table", testTable)
	if err != nil {
		return false, errors.Wrapf(err, "Failed to delete a test table")
	}

	// Check whether in use by parsing ruleset and looking for existing rules.
	ruleset, err := d.nftParseRuleset()
	if err != nil {
		return false, errors.Wrapf(err, "Failed parsing nftables existing ruleset")
	}

	for _, item := range ruleset {
		if item.ItemType == "rule" {
			return true, nil // At least one rule found indicates in use.
		}
	}

	return false, nil
}

// nftGenericItem represents some common fields amongst the different nftables types.
type nftGenericItem struct {
	ItemType string `json:"-"`      // Type of item (table, chain or rule). Populated by LXD.
	Family   string `json:"family"` // Family of item (ip, ip6, bridge etc).
	Table    string `json:"table"`  // Table the item belongs to (for chains and rules).
	Chain    string `json:"chain"`  // Chain the item belongs to (for rules).
	Name     string `json:"name"`   // Name of item (for tables and chains).
}

// nftParseRuleset parses the ruleset and returns the generic parts as a slice of items.
func (d Nftables) nftParseRuleset() ([]nftGenericItem, error) {
	// Dump ruleset as JSON. Use -nn flags to avoid doing DNS lookups of IPs mentioned in any rules.
	cmd := exec.Command("nft", "--json", "-nn", "list", "ruleset")
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
			rule.ItemType = "rule"
			items = append(items, rule)
		} else if chain, found := item["chain"]; found {
			chain.ItemType = "chain"
			items = append(items, chain)
		} else if table, found := item["table"]; found {
			table.ItemType = "table"
			items = append(items, table)
		}
	}

	err = cmd.Wait()
	if err != nil {
		return nil, err
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
	return version.Parse(strings.TrimPrefix(lines[1], "v"))
}

// networkSetupForwardingPolicy allows forwarding dependent on boolean argument
func (d Nftables) networkSetupForwardingPolicy(networkName string, ip4Allow *bool, ip6Allow *bool) error {
	tplFields := map[string]interface{}{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"networkName":    networkName,
		"family":         "inet",
	}

	if ip4Allow != nil {
		ip4Action := "reject"

		if *ip4Allow {
			ip4Action = "accept"
		}

		tplFields["ip4Action"] = ip4Action
	}

	if ip6Allow != nil {
		ip6Action := "reject"

		if *ip6Allow {
			ip6Action = "accept"
		}

		tplFields["ip6Action"] = ip6Action
	}

	err := d.applyNftConfig(nftablesNetForwardingPolicy, tplFields)
	if err != nil {
		return errors.Wrapf(err, "Failed adding forwarding policy rules for network %q (%s)", networkName, tplFields["family"])
	}

	return nil
}

// networkSetupOutboundNAT configures outbound NAT.
// If srcIP is non-nil then SNAT is used with the specified address, otherwise MASQUERADE mode is used.
// Append mode is always on and so the append argument is ignored.
func (d Nftables) networkSetupOutboundNAT(networkName string, SNATV4 *SNATOpts, SNATV6 *SNATOpts) error {
	rules := make(map[string]*SNATOpts, 0)

	tplFields := map[string]interface{}{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"networkName":    networkName,
		"family":         "inet",
	}

	// If SNAT IP not supplied then use the IP of the outbound interface (MASQUERADE).
	if SNATV4 != nil {
		rules["ip"] = SNATV4
	}

	if SNATV6 != nil {
		rules["ip6"] = SNATV6
	}

	tplFields["rules"] = rules

	err := d.applyNftConfig(nftablesNetOutboundNAT, tplFields)
	if err != nil {
		return errors.Wrapf(err, "Failed adding outbound NAT rules for network %q (%s)", networkName, tplFields["family"])
	}

	return nil
}

// networkSetupDHCPDNSAccess sets up basic nftables overrides for DHCP/DNS.
func (d Nftables) networkSetupDHCPDNSAccess(networkName string, ipVersions []uint) error {
	ipFamilies := []string{}
	for _, ipVersion := range ipVersions {
		switch ipVersion {
		case 4:
			ipFamilies = append(ipFamilies, "ip")
		case 6:
			ipFamilies = append(ipFamilies, "ip6")
		}
	}

	tplFields := map[string]interface{}{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"networkName":    networkName,
		"family":         "inet",
		"ipFamilies":     ipFamilies,
	}

	err := d.applyNftConfig(nftablesNetDHCPDNS, tplFields)
	if err != nil {
		return errors.Wrapf(err, "Failed adding DHCP/DNS access rules for network %q (%s)", networkName, tplFields["family"])
	}

	return nil
}

func (d Nftables) networkSetupACLChainAndJumpRules(networkName string) error {
	tplFields := map[string]interface{}{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"networkName":    networkName,
		"family":         "inet",
	}

	config := &strings.Builder{}
	err := nftablesNetACLSetup.Execute(config, tplFields)
	if err != nil {
		return errors.Wrapf(err, "Failed running %q template", nftablesNetACLSetup.Name())
	}

	_, err = shared.RunCommand("nft", config.String())
	if err != nil {
		return err
	}

	return nil
}

// NetworkSetup configure network firewall.
func (d Nftables) NetworkSetup(networkName string, opts Opts) error {
	// Do this first before adding other network rules, so jump to ACL rules come first.
	if opts.ACL {
		err := d.networkSetupACLChainAndJumpRules(networkName)
		if err != nil {
			return err
		}
	}

	if opts.SNATV4 != nil || opts.SNATV6 != nil {
		err := d.networkSetupOutboundNAT(networkName, opts.SNATV4, opts.SNATV6)
		if err != nil {
			return err
		}
	}

	dhcpDNSAccess := []uint{}
	var ip4ForwardingAllow, ip6ForwardingAllow *bool

	if opts.FeaturesV4 != nil || opts.FeaturesV6 != nil {
		if opts.FeaturesV4 != nil {
			if opts.FeaturesV4.DHCPDNSAccess {
				dhcpDNSAccess = append(dhcpDNSAccess, 4)
			}

			ip4ForwardingAllow = &opts.FeaturesV4.ForwardingAllow
		}

		if opts.FeaturesV6 != nil {
			if opts.FeaturesV6.DHCPDNSAccess {
				dhcpDNSAccess = append(dhcpDNSAccess, 6)
			}

			ip6ForwardingAllow = &opts.FeaturesV6.ForwardingAllow
		}

		err := d.networkSetupForwardingPolicy(networkName, ip4ForwardingAllow, ip6ForwardingAllow)
		if err != nil {
			return err
		}

		err = d.networkSetupDHCPDNSAccess(networkName, dhcpDNSAccess)
		if err != nil {
			return err
		}
	}

	return nil
}

// NetworkClear removes the LXD network related chains.
// The delete and ipeVersions arguments have no effect for nftables driver.
func (d Nftables) NetworkClear(networkName string, _ bool, _ []uint) error {
	// Remove chains created by network rules.
	// Remove from ip and ip6 tables to ensure cleanup for instances started before we moved to inet table.
	err := d.removeChains([]string{"inet", "ip", "ip6"}, networkName, "fwd", "pstrt", "in", "out", "aclin", "aclout", "aclfwd", "acl")
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
func (d Nftables) InstanceSetupBridgeFilter(projectName string, instanceName string, deviceName string, parentName string, hostName string, hwAddr string, IPv4 net.IP, IPv6 net.IP) error {
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

	// Filter unwanted ethernet frames when using IP filtering.
	if IPv4 != nil || IPv6 != nil {
		tplFields["filterUnwantedFrames"] = true
	}

	if IPv4 != nil {
		if IPv4.String() == FilterIPv4All {
			tplFields["ipv4FilterAll"] = true
		} else {
			tplFields["ipv4Addr"] = IPv4.String()
		}
	}

	if IPv6 != nil {
		if IPv6.String() == FilterIPv6All {
			tplFields["ipv6FilterAll"] = true
		} else {
			tplFields["ipv6Addr"] = IPv6.String()
			tplFields["ipv6AddrHex"] = fmt.Sprintf("0x%s", hex.EncodeToString(IPv6))
		}
	}

	err = d.applyNftConfig(nftablesInstanceBridgeFilter, tplFields)
	if err != nil {
		return errors.Wrapf(err, "Failed adding bridge filter rules for instance device %q (%s)", deviceLabel, tplFields["family"])
	}

	return nil
}

// InstanceClearBridgeFilter removes any filter rules that were added to apply bridged device IP filtering.
func (d Nftables) InstanceClearBridgeFilter(projectName string, instanceName string, deviceName string, parentName string, hostName string, hwAddr string, _ net.IP, _ net.IP) error {
	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)

	// Remove chains created by bridge filter rules.
	err := d.removeChains([]string{"bridge"}, deviceLabel, "in", "fwd")
	if err != nil {
		return errors.Wrapf(err, "Failed clearing bridge filter rules for instance device %q", deviceLabel)
	}

	return nil
}

// InstanceSetupProxyNAT creates DNAT rules for proxy devices.
func (d Nftables) InstanceSetupProxyNAT(projectName string, instanceName string, deviceName string, listen, connect *deviceConfig.ProxyAddress) error {
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
		ipFamily := "ip"
		connectDest := fmt.Sprintf("%s:%s", connectHost, connectPort)
		connectIP := net.ParseIP(connectHost)
		if connectIP.To4() == nil {
			ipFamily = "ip6"
			connectDest = fmt.Sprintf("[%s]:%s", connectHost, connectPort)
		}

		rules = append(rules, map[string]interface{}{
			"family":        "inet",
			"ipFamily":      ipFamily,
			"connType":      listen.ConnType,
			"listenHost":    listenHost,
			"listenPort":    listenPort,
			"connectDest":   connectDest,
			"connectHost":   connectHost,
			"connectPort":   connectPort,
			"addHairpinNat": connectIndex == i, // Only add >1 hairpin NAT rules if connect range used.
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
func (d Nftables) InstanceClearProxyNAT(projectName string, instanceName string, deviceName string) error {
	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)

	// Remove from ip and ip6 tables to ensure cleanup for instances started before we moved to inet table.
	err := d.removeChains([]string{"inet", "ip", "ip6"}, deviceLabel, "out", "prert", "pstrt")
	if err != nil {
		return errors.Wrapf(err, "Failed clearing proxy rules for instance device %q", deviceLabel)
	}

	return nil
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

	// Search ruleset for chains we are looking for.
	foundChains := make(map[string]nftGenericItem)
	for _, family := range families {
		for _, item := range ruleset {
			if item.ItemType == "chain" && item.Family == family && item.Table == nftablesNamespace && shared.StringInSlice(item.Name, fullChains) {
				foundChains[item.Name] = item
			}
		}
	}

	// Delete the chains in the order specified in chains slice (to avoid dependency issues).
	for _, fullChain := range fullChains {
		item, found := foundChains[fullChain]
		if !found {
			continue
		}

		_, err = shared.RunCommand("nft", "flush", "chain", item.Family, nftablesNamespace, item.Name, ";", "delete", "chain", item.Family, nftablesNamespace, item.Name)
		if err != nil {
			return errors.Wrapf(err, "Failed deleting nftables chain %q (%s)", item.Name, item.Family)
		}
	}

	return nil
}

// InstanceSetupRPFilter activates reverse path filtering for the specified instance device on the host interface.
func (d Nftables) InstanceSetupRPFilter(projectName string, instanceName string, deviceName string, hostName string) error {
	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)
	tplFields := map[string]interface{}{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"deviceLabel":    deviceLabel,
		"hostName":       hostName,
		"family":         "inet",
	}

	err := d.applyNftConfig(nftablesInstanceRPFilter, tplFields)
	if err != nil {
		return errors.Wrapf(err, "Failed adding reverse path filter rules for instance device %q (%s)", deviceLabel, tplFields["family"])
	}

	return nil
}

// InstanceClearRPFilter removes reverse path filtering for the specified instance device on the host interface.
func (d Nftables) InstanceClearRPFilter(projectName string, instanceName string, deviceName string) error {
	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)

	// Remove from ip and ip6 tables to ensure cleanup for instances started before we moved to inet table.
	err := d.removeChains([]string{"inet", "ip", "ip6"}, deviceLabel, "prert")
	if err != nil {
		return errors.Wrapf(err, "Failed clearing reverse path filter rules for instance device %q", deviceLabel)
	}

	return nil
}

// NetworkApplyACLRules applies ACL rules to the existing firewall chains.
func (d Nftables) NetworkApplyACLRules(networkName string, rules []ACLRule) error {
	nftRules := make([]string, 0)
	for _, rule := range rules {
		// First try generating rules with IPv4 or IP agnostic criteria.
		nftRule, partial, err := d.aclRuleCriteriaToRules(networkName, 4, &rule)
		if err != nil {
			return err
		}

		if nftRule != "" {
			nftRules = append(nftRules, nftRule)
		}

		if partial {
			// If we couldn't fully generate the ruleset with only IPv4 or IP agnostic criteria, then
			// fill in the remaining parts using IPv6 criteria.
			nftRule, _, err = d.aclRuleCriteriaToRules(networkName, 6, &rule)
			if err != nil {
				return err
			}

			if nftRule == "" {
				return fmt.Errorf("Invalid empty rule generated")
			}

			nftRules = append(nftRules, nftRule)
		} else if nftRule == "" {
			return fmt.Errorf("Invalid empty rule generated")
		}
	}

	tplFields := map[string]interface{}{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"networkName":    networkName,
		"family":         "inet",
		"rules":          nftRules,
	}
	config := &strings.Builder{}
	err := nftablesNetACLRules.Execute(config, tplFields)
	if err != nil {
		return errors.Wrapf(err, "Failed running %q template", nftablesNetACLRules.Name())
	}

	_, err = shared.RunCommand("nft", config.String())
	if err != nil {
		return err
	}

	return nil
}

// aclRuleCriteriaToRules converts an ACL rule into 1 or more nftables rules.
func (d Nftables) aclRuleCriteriaToRules(networkName string, ipVersion uint, rule *ACLRule) (string, bool, error) {
	var args []string

	if rule.Direction == "ingress" {
		args = append(args, "oifname", networkName) // Coming from host into network's interface.
	} else {
		args = append(args, "iifname", networkName) // Coming from network's interface into host.
	}

	// Add subject filters.
	isPartialRule := false

	if rule.Source != "" {
		matchArgs, partial, err := d.aclRuleSubjectToACLMatch("saddr", ipVersion, util.SplitNTrimSpace(rule.Source, ",", -1, false)...)
		if err != nil {
			return "", false, err
		}

		if matchArgs == nil {
			return "", true, nil // Rule is not appropriate for ipVersion.
		}

		if partial && isPartialRule == false {
			isPartialRule = true
		}

		args = append(args, matchArgs...)
	}

	if rule.Destination != "" {
		matchArgs, partial, err := d.aclRuleSubjectToACLMatch("daddr", ipVersion, util.SplitNTrimSpace(rule.Destination, ",", -1, false)...)
		if err != nil {
			return "", false, err
		}

		if matchArgs == nil {
			return "", partial, nil // Rule is not appropriate for ipVersion.
		}

		if partial && isPartialRule == false {
			isPartialRule = true
		}

		args = append(args, matchArgs...)
	}

	// Add protocol filters.
	if shared.StringInSlice(rule.Protocol, []string{"tcp", "udp"}) {
		args = append(args, "meta", "l4proto", rule.Protocol)

		if rule.SourcePort != "" {
			args = append(args, d.aclRulePortToACLMatch("sport", util.SplitNTrimSpace(rule.SourcePort, ",", -1, false)...)...)
		}

		if rule.DestinationPort != "" {
			args = append(args, d.aclRulePortToACLMatch("dport", util.SplitNTrimSpace(rule.DestinationPort, ",", -1, false)...)...)
		}
	} else if shared.StringInSlice(rule.Protocol, []string{"icmp4", "icmp6"}) {
		var icmpIPVersion uint
		var protoName string

		switch rule.Protocol {
		case "icmp4":
			protoName = "icmp"
			icmpIPVersion = 4
			args = append(args, "ip", "protocol", protoName)
		case "icmp6":
			protoName = "icmpv6"
			icmpIPVersion = 6
			args = append(args, "ip6", "nexthdr", protoName)
		}

		if ipVersion != icmpIPVersion {
			// If we got this far it means that source/destination are either empty or are filled
			// with at least some subjects in the same family as ipVersion. So if the icmpIPVersion
			// doesn't match the ipVersion then it means the rule contains mixed-version subjects
			// which is invalid when using an IP version specific ICMP protocol.
			if rule.Source != "" || rule.Destination != "" {
				return "", false, fmt.Errorf("Invalid use of %q protocol with non-IPv%d source/destination criteria", rule.Protocol, ipVersion)
			}

			// Otherwise it means this is just a blanket ICMP rule and is only appropriate for use
			// with the corresponding ipVersion nft command.
			return "", true, nil // Rule is not appropriate for ipVersion.
		}

		if rule.ICMPType != "" {
			args = append(args, protoName, "type", rule.ICMPType)

			if rule.ICMPCode != "" {
				args = append(args, protoName, "code", rule.ICMPCode)
			}
		}
	}

	// Handle logging.
	if rule.Log {
		args = append(args, "log")

		if rule.LogName != "" {
			// Add a trailing space to prefix for readability in logs.
			args = append(args, "prefix", fmt.Sprintf(`"%s "`, rule.LogName))
		}
	}

	// Handle action.
	action := rule.Action
	if action == "allow" {
		action = "accept"
	}

	args = append(args, action)

	return strings.Join(args, " "), isPartialRule, nil
}

// aclRuleSubjectToACLMatch converts direction (source/destination) and subject criteria list into xtables args.
// Returns nil if none of the subjects are appropriate for the ipVersion.
func (d Nftables) aclRuleSubjectToACLMatch(direction string, ipVersion uint, subjectCriteria ...string) ([]string, bool, error) {
	fieldParts := make([]string, 0, len(subjectCriteria))

	partial := false

	// For each criterion check if value looks like IP CIDR.
	for _, subjectCriterion := range subjectCriteria {
		if validate.IsNetworkRange(subjectCriterion) == nil {
			criterionParts := strings.SplitN(subjectCriterion, "-", 2)
			if len(criterionParts) > 1 {
				ip := net.ParseIP(criterionParts[0])
				if ip != nil {
					var subjectIPVersion uint = 4
					if ip.To4() == nil {
						subjectIPVersion = 6
					}

					if ipVersion != subjectIPVersion {
						partial = true
						continue // Skip subjects that are not for the ipVersion we are looking for.
					}

					fieldParts = append(fieldParts, fmt.Sprintf("%s-%s", criterionParts[0], criterionParts[1]))
				}
			} else {
				return nil, false, fmt.Errorf("Invalid IP range %q", subjectCriterion)
			}
		} else {
			ip := net.ParseIP(subjectCriterion)
			if ip == nil {
				ip, _, _ = net.ParseCIDR(subjectCriterion)
			}

			if ip != nil {
				var subjectIPVersion uint = 4
				if ip.To4() == nil {
					subjectIPVersion = 6
				}

				if ipVersion != subjectIPVersion {
					partial = true
					continue // Skip subjects that are not for the ipVersion we are looking for.
				}

				fieldParts = append(fieldParts, subjectCriterion)
			} else {
				return nil, false, fmt.Errorf("Unsupported nftables subject %q", subjectCriterion)
			}
		}
	}

	if len(fieldParts) > 0 {
		ipFamily := "ip"
		if ipVersion == 6 {
			ipFamily = "ip6"
		}

		return []string{ipFamily, direction, fmt.Sprintf("{%s}", strings.Join(fieldParts, ","))}, partial, nil
	}

	return nil, partial, nil // No subjects suitable for ipVersion.
}

// aclRulePortToACLMatch converts protocol (tcp/udp), direction (sports/dports) and port criteria list into
// xtables args.
func (d Nftables) aclRulePortToACLMatch(direction string, portCriteria ...string) []string {
	fieldParts := make([]string, 0, len(portCriteria))

	for _, portCriterion := range portCriteria {
		criterionParts := strings.SplitN(portCriterion, "-", 2)
		if len(criterionParts) > 1 {
			fieldParts = append(fieldParts, fmt.Sprintf("%s-%s", criterionParts[0], criterionParts[1]))
		} else {
			fieldParts = append(fieldParts, fmt.Sprintf("%s", criterionParts[0]))
		}
	}

	return []string{"th", direction, fmt.Sprintf("{%s}", strings.Join(fieldParts, ","))}
}
