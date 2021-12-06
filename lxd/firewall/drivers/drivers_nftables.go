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

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/shared"
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

// networkSetupICMPDHCPDNSAccess sets up basic nftables overrides for ICMP, DHCP and DNS.
func (d Nftables) networkSetupICMPDHCPDNSAccess(networkName string, ipVersions []uint) error {
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

	err := d.applyNftConfig(nftablesNetICMPDHCPDNS, tplFields)
	if err != nil {
		return errors.Wrapf(err, "Failed adding ICMP, DHCP and DNS access rules for network %q (%s)", networkName, tplFields["family"])
	}

	return nil
}

// NetworkSetup configure network firewall.
func (d Nftables) NetworkSetup(networkName string, opts Opts) error {
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
			if opts.FeaturesV4.ICMPDHCPDNSAccess {
				dhcpDNSAccess = append(dhcpDNSAccess, 4)
			}

			ip4ForwardingAllow = &opts.FeaturesV4.ForwardingAllow
		}

		if opts.FeaturesV6 != nil {
			if opts.FeaturesV6.ICMPDHCPDNSAccess {
				dhcpDNSAccess = append(dhcpDNSAccess, 6)
			}

			ip6ForwardingAllow = &opts.FeaturesV6.ForwardingAllow
		}

		err := d.networkSetupForwardingPolicy(networkName, ip4ForwardingAllow, ip6ForwardingAllow)
		if err != nil {
			return err
		}

		err = d.networkSetupICMPDHCPDNSAccess(networkName, dhcpDNSAccess)
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
	err := d.removeChains([]string{"inet", "ip", "ip6"}, networkName, "fwd", "pstrt", "in", "out")
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
func (d Nftables) InstanceSetupBridgeFilter(projectName string, instanceName string, deviceName string, parentName string, hostName string, hwAddr string, IPv4 net.IP, IPv6 net.IP, _ bool) error {
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
func (d Nftables) InstanceSetupProxyNAT(projectName string, instanceName string, deviceName string, forward *AddressForward) error {
	if forward.ListenAddress == nil {
		return fmt.Errorf("Listen address is required")
	}

	if forward.TargetAddress == nil {
		return fmt.Errorf("Target address is required")
	}

	listenPortsLen := len(forward.ListenPorts)
	if listenPortsLen <= 0 {
		return fmt.Errorf("At least 1 listen port must be supplied")
	}

	// If multiple target ports supplied, check they match the listen port(s) count.
	targetPortsLen := len(forward.TargetPorts)
	if targetPortsLen != 1 && targetPortsLen != listenPortsLen {
		return fmt.Errorf("Mismatch between listen port(s) and target port(s) count")
	}

	ipFamily := "ip"
	if forward.ListenAddress.To4() == nil {
		ipFamily = "ip6"
	}

	listenAddressStr := forward.ListenAddress.String()
	targetAddressStr := forward.TargetAddress.String()

	// Generate slices of rules to add.
	var dnatRules []map[string]interface{}
	var snatRules []map[string]interface{}

	targetPortRanges := portRangesFromSlice(forward.TargetPorts)
	for _, targetPortRange := range targetPortRanges {
		targetPortRangeStr := portRangeStr(targetPortRange, "-")
		snatRules = append(snatRules, map[string]interface{}{
			"ipFamily":    ipFamily,
			"protocol":    forward.Protocol,
			"targetHost":  targetAddressStr,
			"targetPorts": targetPortRangeStr,
		})
	}

	dnatRanges := getOptimisedDNATRanges(forward)
	for listenPortRange, targetPortRange := range dnatRanges {
		// Format the destination host/port as appropriate
		targetDest := targetAddressStr
		if targetPortRange[1] == 1 {
			targetPortStr := portRangeStr(targetPortRange, ":")
			targetDest = fmt.Sprintf("%s:%s", targetAddressStr, targetPortStr)
			if ipFamily == "ip6" {
				targetDest = fmt.Sprintf("[%s]:%s", targetAddressStr, targetPortStr)
			}
		}

		dnatRules = append(dnatRules, map[string]interface{}{
			"ipFamily":      ipFamily,
			"protocol":      forward.Protocol,
			"listenAddress": listenAddressStr,
			"listenPorts":   portRangeStr(listenPortRange, "-"),
			"targetDest":    targetDest,
		})
	}

	deviceLabel := d.instanceDeviceLabel(projectName, instanceName, deviceName)
	tplFields := map[string]interface{}{
		"namespace":      nftablesNamespace,
		"chainSeparator": nftablesChainSeparator,
		"chainPrefix":    "", // Empty prefix for backwards compatibility with existing device chains.
		"family":         "inet",
		"label":          deviceLabel,
		"dnatRules":      dnatRules,
		"snatRules":      snatRules,
	}

	config := &strings.Builder{}
	err := nftablesNetProxyNAT.Execute(config, tplFields)
	if err != nil {
		return errors.Wrapf(err, "Failed running %q template", nftablesNetProxyNAT.Name())
	}

	_, err = shared.RunCommand("nft", config.String())
	if err != nil {
		return err
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

	for _, family := range families {
		for _, item := range ruleset {
			if item.ItemType == "chain" && item.Family == family && item.Table == nftablesNamespace && shared.StringInSlice(item.Name, fullChains) {
				_, err = shared.RunCommand("nft", "flush", "chain", family, nftablesNamespace, item.Name, ";", "delete", "chain", family, nftablesNamespace, item.Name)
				if err != nil {
					return errors.Wrapf(err, "Failed deleting nftables chain %q (%s)", item.Name, family)
				}
			}
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
