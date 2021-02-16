package acl

import (
	"fmt"
	"net"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/network/openvswitch"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/validate"
)

// OVN ACL rule priorities.
const ovnACLPrioritySwitchAllow = 10
const ovnACLPriorityPortGroupDefaultDrop = 0
const ovnACLPriorityPortGroupAllow = 20
const ovnACLPriorityPortGroupReject = 30
const ovnACLPriorityPortGroupDrop = 40

// OVNACLPortGroupName returns the port group name for a Network ACL ID.
func OVNACLPortGroupName(networkACLID int64) openvswitch.OVNPortGroup {
	// OVN doesn't match port groups that have a "-" in them. So use an "_" for the separator.
	return openvswitch.OVNPortGroup(fmt.Sprintf("lxd_acl%d", networkACLID))
}

// OVNApplyToPortGroup applies the rules in the specified ACL to the specified port group.
func OVNApplyToPortGroup(s *state.State, client *openvswitch.OVN, aclInfo *api.NetworkACL, portGroupName openvswitch.OVNPortGroup, aclNameIDs map[string]int64) error {
	// Create slice for port group rules that has the capacity for ingress and egress rules, plus default drop.
	portGroupRules := make([]openvswitch.OVNACLRule, 0, len(aclInfo.Ingress)+len(aclInfo.Egress)+1)

	// convertACLRules converts the ACL rules to OVN ACL rules.
	convertACLRules := func(direction string, rules ...api.NetworkACLRule) error {
		for ruleIndex, rule := range rules {
			if rule.State == "disabled" {
				continue
			}

			portGroupRule, err := ovnRuleCriteriaToOVNPortGroupRule(direction, &rule, portGroupName, aclNameIDs)
			if err != nil {
				return err
			}

			if rule.State == "logged" {
				portGroupRule.Log = true
				portGroupRule.LogName = fmt.Sprintf("%s-%s-%d", portGroupName, direction, ruleIndex)
			}

			portGroupRules = append(portGroupRules, portGroupRule)
		}

		return nil
	}

	err := convertACLRules("ingress", aclInfo.Ingress...)
	if err != nil {
		return errors.Wrapf(err, "Failed converting ACL %q ingress rules for port group %q", aclInfo.Name, portGroupName)
	}

	err = convertACLRules("egress", aclInfo.Egress...)
	if err != nil {
		return errors.Wrapf(err, "Failed converting ACL %q egress rules for port group %q", aclInfo.Name, portGroupName)
	}

	// Add default drop rule to port group ACL.
	portGroupRules = append(portGroupRules, openvswitch.OVNACLRule{
		Direction: "to-lport", // Always use this so that outport is available to Match.
		Action:    "drop",
		Priority:  0, // Lowest priority to catch only unmatched traffic.
		Match:     fmt.Sprintf("inport == @%s || outport == @%s", portGroupName, portGroupName),
		Log:       true,
		LogName:   string(portGroupName),
	})

	err = client.PortGroupSetACLRules(portGroupName, portGroupRules...)
	if err != nil {
		return errors.Wrapf(err, "Failed applying ACL %q rules to port group %q", aclInfo.Name, portGroupName)
	}

	return nil
}

// ovnRuleCriteriaToOVNPortGroupRule converts an ACL rule into an OVNACLRule for a port group.
func ovnRuleCriteriaToOVNPortGroupRule(direction string, rule *api.NetworkACLRule, portGroupName openvswitch.OVNPortGroup, aclNameIDs map[string]int64) (openvswitch.OVNACLRule, error) {
	portGroupRule := openvswitch.OVNACLRule{
		Direction: "to-lport", // Always use this so that outport is available to Match.
	}

	// Populate Action and Priority based on rule's Action.
	switch rule.Action {
	case "allow":
		portGroupRule.Action = "allow-related" // TODO add stateless support.
		portGroupRule.Priority = ovnACLPriorityPortGroupAllow
	case "reject":
		portGroupRule.Action = "reject"
		portGroupRule.Priority = ovnACLPriorityPortGroupReject
	case "drop":
		portGroupRule.Action = "drop"
		portGroupRule.Priority = ovnACLPriorityPortGroupDrop
	}

	var matchParts []string

	// Add directional port filter so we only apply this rule to the ports in the port group.
	switch direction {
	case "ingress":
		matchParts = []string{fmt.Sprintf("outport == @%s", portGroupName)} // Traffic going to Instance.
	case "egress":
		matchParts = []string{fmt.Sprintf("inport == @%s", portGroupName)} // Traffic leaving Instance.
	default:
		matchParts = []string{fmt.Sprintf("inport == @%s || outport == @%s", portGroupName, portGroupName)}
	}

	// Add subject filters.
	if rule.Source != "" {
		match, err := ovnRuleSubjectToOVNACLMatch("src", aclNameIDs, util.SplitNTrimSpace(rule.Source, ",", -1, false)...)
		if err != nil {
			return openvswitch.OVNACLRule{}, err
		}

		matchParts = append(matchParts, match)
	}

	if rule.Destination != "" {
		match, err := ovnRuleSubjectToOVNACLMatch("dst", aclNameIDs, util.SplitNTrimSpace(rule.Destination, ",", -1, false)...)
		if err != nil {
			return openvswitch.OVNACLRule{}, err
		}

		matchParts = append(matchParts, match)
	}

	// Add protocol filters.
	if shared.StringInSlice(rule.Protocol, []string{"tcp", "udp"}) {
		matchParts = append(matchParts, fmt.Sprintf("%s", rule.Protocol))

		if rule.SourcePort != "" {
			matchParts = append(matchParts, ovnRulePortToOVNACLMatch(rule.Protocol, "src", util.SplitNTrimSpace(rule.SourcePort, ",", -1, false)...))
		}

		if rule.DestinationPort != "" {
			matchParts = append(matchParts, ovnRulePortToOVNACLMatch(rule.Protocol, "dst", util.SplitNTrimSpace(rule.DestinationPort, ",", -1, false)...))
		}
	} else if shared.StringInSlice(rule.Protocol, []string{"icmp4", "icmp6"}) {
		matchParts = append(matchParts, fmt.Sprintf("%s", rule.Protocol))

		if rule.ICMPType != "" {
			matchParts = append(matchParts, fmt.Sprintf("%s.type == %s", rule.Protocol, rule.ICMPType))
		}

		if rule.ICMPCode != "" {
			matchParts = append(matchParts, fmt.Sprintf("%s.code == %s", rule.Protocol, rule.ICMPCode))
		}
	}

	// Populate the Match field with the generated match parts.
	portGroupRule.Match = fmt.Sprintf("(%s)", strings.Join(matchParts, ") && ("))

	return portGroupRule, nil
}

// ovnRulePortToOVNACLMatch converts protocol (tcp/udp), direction (src/dst) and port criteria list into an OVN
// match statement.
func ovnRulePortToOVNACLMatch(protocol string, direction string, portCriteria ...string) string {
	fieldParts := make([]string, 0, len(portCriteria))

	for _, portCriterion := range portCriteria {
		criterionParts := strings.SplitN(portCriterion, "-", 2)
		if len(criterionParts) > 1 {
			fieldParts = append(fieldParts, fmt.Sprintf("(%s.%s >= %s && %s.%s <= %s)", protocol, direction, criterionParts[0], protocol, direction, criterionParts[1]))
		} else {
			fieldParts = append(fieldParts, fmt.Sprintf("%s.%s == %s", protocol, direction, criterionParts[0]))
		}
	}

	return strings.Join(fieldParts, " || ")
}

// ovnRuleSubjectToOVNACLMatch converts direction (src/dst) and subject criteria list into an OVN match statement.
func ovnRuleSubjectToOVNACLMatch(direction string, aclNameIDs map[string]int64, subjectCriteria ...string) (string, error) {
	fieldParts := make([]string, 0, len(subjectCriteria))

	// For each criterion check if value looks like an IP range or IP CIDR, and if not use it as an ACL name.
	for _, subjectCriterion := range subjectCriteria {
		if validate.IsNetworkRange(subjectCriterion) == nil {
			criterionParts := strings.SplitN(subjectCriterion, "-", 2)
			if len(criterionParts) > 1 {
				ip := net.ParseIP(criterionParts[0])
				if ip != nil {
					protocol := "ip4"
					if ip.To4() == nil {
						protocol = "ip6"
					}

					fieldParts = append(fieldParts, fmt.Sprintf("(%s.%s >= %s && %s.%s <= %s)", protocol, direction, criterionParts[0], protocol, direction, criterionParts[1]))
				}
			} else {
				return "", fmt.Errorf("Invalid IP range %q", subjectCriterion)
			}
		} else {
			ip, _, err := net.ParseCIDR(subjectCriterion)
			if err == nil {
				protocol := "ip4"
				if ip.To4() == nil {
					protocol = "ip6"
				}

				fieldParts = append(fieldParts, fmt.Sprintf("%s.%s == %s", protocol, direction, subjectCriterion))
			} else {
				// If not valid IP subnet, then assume this is an OVN port group name.
				portType := "inport"
				if direction == "dst" {
					portType = "outport"
				}

				aclID, found := aclNameIDs[subjectCriterion]
				if !found {
					return "", fmt.Errorf("Cannot find security ACL ID for %q", subjectCriterion)
				}

				fieldParts = append(fieldParts, fmt.Sprintf("%s == @%s", portType, OVNACLPortGroupName(aclID)))
			}
		}
	}

	return strings.Join(fieldParts, " || "), nil
}

// OVNApplyNetworkBaselineRules applies preset baseline logical switch rules to a allow access to network services.
func OVNApplyNetworkBaselineRules(client *openvswitch.OVN, switchName openvswitch.OVNSwitch, routerPortName openvswitch.OVNSwitchPort, intRouterIPs []*net.IPNet, dnsIPs []net.IP) error {
	rules := []openvswitch.OVNACLRule{
		{
			Direction: "to-lport",
			Action:    "allow",
			Priority:  ovnACLPrioritySwitchAllow,
			Match:     "arp || nd", // Neighbour discovery.
		},
		{
			Direction: "to-lport",
			Action:    "allow",
			Priority:  ovnACLPrioritySwitchAllow,
			Match:     fmt.Sprintf(`inport == "%s" && nd_ra`, routerPortName), // IPv6 router adverts from router.
		},
		{
			Direction: "to-lport",
			Action:    "allow",
			Priority:  ovnACLPrioritySwitchAllow,
			Match:     fmt.Sprintf(`outport == "%s" && nd_rs`, routerPortName), // IPv6 router solicitation to router.
		},
		{
			Direction: "to-lport",
			Action:    "allow",
			Priority:  ovnACLPrioritySwitchAllow,
			Match:     fmt.Sprintf(`outport == "%s" && ((ip4 && udp.dst == 67) || (ip6 && udp.dst == 547)) `, routerPortName), // DHCP to router.
		},
	}

	// Add rules to allow ping to/from internal router IPs.
	for _, intRouterIP := range intRouterIPs {
		ipVersion := 4
		icmpPingType := 8
		icmpPingReplyType := 0
		if intRouterIP.IP.To4() == nil {
			ipVersion = 6
			icmpPingType = 128
			icmpPingReplyType = 129
		}

		rules = append(rules,
			openvswitch.OVNACLRule{
				Direction: "to-lport",
				Action:    "allow",
				Priority:  ovnACLPrioritySwitchAllow,
				Match:     fmt.Sprintf(`outport == "%s" && icmp%d.type == %d && ip%d.dst == %s`, routerPortName, ipVersion, icmpPingType, ipVersion, intRouterIP.IP),
			},
			openvswitch.OVNACLRule{
				Direction: "to-lport",
				Action:    "allow",
				Priority:  ovnACLPrioritySwitchAllow,
				Match:     fmt.Sprintf(`inport == "%s" && icmp%d.type == %d && ip%d.src == %s`, routerPortName, ipVersion, icmpPingReplyType, ipVersion, intRouterIP.IP),
			},
		)
	}

	// Add rules to allow DNS to DNS IPs.
	for _, dnsIP := range dnsIPs {
		ipVersion := 4
		if dnsIP.To4() == nil {
			ipVersion = 6
		}

		rules = append(rules,
			openvswitch.OVNACLRule{
				Direction: "to-lport",
				Action:    "allow",
				Priority:  ovnACLPrioritySwitchAllow,
				Match:     fmt.Sprintf(`outport == "%s" && ip%d.dst == %s && (udp.dst == 53 || tcp.dst == 53)`, routerPortName, ipVersion, dnsIP),
			},
		)
	}

	err := client.LogicalSwitchSetACLRules(switchName, rules...)
	if err != nil {
		return errors.Wrapf(err, "Failed applying baseline ACL rules to logical switch %q", switchName)
	}

	return nil
}
