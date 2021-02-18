package acl

import (
	"fmt"
	"net"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/network/openvswitch"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/validate"
)

// OVN ACL rule priorities.
const ovnACLPrioritySwitchAllow = 10
const ovnACLPriorityPortGroupAllow = 20
const ovnACLPriorityPortGroupReject = 30
const ovnACLPriorityPortGroupDrop = 40

// OVNACLPortGroupName returns the port group name for a Network ACL ID.
func OVNACLPortGroupName(networkACLID int64) openvswitch.OVNPortGroup {
	// OVN doesn't match port groups that have a "-" in them. So use an "_" for the separator.
	return openvswitch.OVNPortGroup(fmt.Sprintf("lxd_acl%d", networkACLID))
}

// OVNEnsureACLs ensures that the requested aclNames exist as OVN port groups (creates & applies ACL rules if not),
// and adds the requested addMembers to the new or existing OVN port groups. If forceApplyRules is true then the
// current ACL rules in the database are applied to the existing port groups rather than just new ones.
func OVNEnsureACLs(s *state.State, logger logger.Logger, client *openvswitch.OVN, aclProjectName string, aclNameIDs map[string]int64, aclNames []string, reapplyRules bool, addMembers ...openvswitch.OVNSwitchPort) (*revert.Reverter, error) {
	return ovnEnsureACLs(s, logger, client, aclProjectName, aclNameIDs, aclNames, reapplyRules, true, addMembers...)
}

// ovnEnsureACLs unexported version of OVNEnsureACLs with recurse argument.
func ovnEnsureACLs(s *state.State, logger logger.Logger, client *openvswitch.OVN, aclProjectName string, aclNameIDs map[string]int64, aclNames []string, reapplyRules bool, recurse bool, addMembers ...openvswitch.OVNSwitchPort) (*revert.Reverter, error) {
	revert := revert.New()
	defer revert.Fail()

	// First check all ACL Names map to IDs in supplied aclNameIDs.
	for _, aclName := range aclNames {
		_, found := aclNameIDs[aclName]
		if !found {
			return nil, fmt.Errorf("Cannot find security ACL ID for %q", aclName)
		}
	}

	// Next check which OVN port groups need creating and which exist already.
	createACLPortGroups := []string{}
	existingACLPortGroups := []string{}
	for _, aclName := range aclNames {
		portGroupName := OVNACLPortGroupName(aclNameIDs[aclName])

		// Check if port group exists.
		portGroupUUID, _, err := client.PortGroupInfo(portGroupName)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed getting port group UUID for security ACL %q setup", aclName)
		}

		if portGroupUUID == "" {
			createACLPortGroups = append(createACLPortGroups, aclName)
		} else {
			existingACLPortGroups = append(existingACLPortGroups, aclName)
		}
	}

	// Retrieve instance port UUIDs to add to existing port groups if needed.
	var memberUUIDs map[openvswitch.OVNSwitchPort]openvswitch.OVNSwitchPortUUID
	if len(aclNames) > len(createACLPortGroups) {
		memberUUIDs = make(map[openvswitch.OVNSwitchPort]openvswitch.OVNSwitchPortUUID, len(aclNames)-len(createACLPortGroups))

		for _, memberName := range addMembers {
			// Get logical port UUID.
			portUUID, err := client.LogicalSwitchPortUUID(memberName)
			if err != nil || portUUID == "" {
				return nil, errors.Wrapf(err, "Failed getting logical port UUID for %q for security ACL setup", memberName)
			}

			memberUUIDs[memberName] = portUUID
		}
	}

	// Create the needed port groups (and add requested members), then apply ACL rules to new port group.
	for _, aclName := range createACLPortGroups {
		portGroupName := OVNACLPortGroupName(aclNameIDs[aclName])
		logger.Debug("Creating ACL OVN port group", log.Ctx{"networkACL": aclName, "portGroup": portGroupName, "addMembers": addMembers})

		err := client.PortGroupAdd(portGroupName, addMembers...)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed creating port group %q for security ACL %q setup", portGroupName, aclName)
		}
		revert.Add(func() { client.PortGroupDelete(portGroupName) })

		_, aclInfo, err := s.Cluster.GetNetworkACL(aclProjectName, aclName)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed loading Network ACL %q", aclName)
		}

		if recurse {
			// If recursion is enabled, before applying rules to port group, check if the rules
			// reference any other ACLs, and ensure they exist as OVN port groups first.
			// Note: This is to avoid OVN error messages in the logs complaining that a port group
			// referenced in our ACL rules doesn't exist. However it is not completely full-proof as
			// if there is a circular dependency between ACLs then one of them may cause error logs
			// briefly until the other is created.

			// Preload ourself into referenced URLs, as we know we are going to ensure ourselves below.
			referencedACLs := map[string]*api.NetworkACL{aclInfo.Name: aclInfo}

			// Now recursively find any other ACLs referenced, and build up an authoritative list.
			err = ovnAddReferencedACLs(s, aclProjectName, aclInfo, referencedACLs)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed finding referenced ACLs for Network ACL %q", aclName)
			}

			// Remove ourselves now recursive check is complete, so we don't ensure ourselves twice.
			delete(referencedACLs, aclInfo.Name)

			// Ensure any referenced ACLs are setup.
			if len(referencedACLs) > 0 {
				ensureReferencedACLs := make([]string, 0, len(referencedACLs))
				for aclName := range referencedACLs {
					ensureReferencedACLs = append(ensureReferencedACLs, aclName)
				}

				r, err := ovnEnsureACLs(s, logger, client, aclProjectName, aclNameIDs, ensureReferencedACLs, false, false)
				if err != nil {
					return revert, errors.Wrapf(err, "Failed ensuring referenced security ACLs are configured in OVN for ACL %q", aclName)
				}
				revert.Add(r.Fail)
			}
		}

		// Now apply our ACL rules to port group.
		err = ovnApplyToPortGroup(s, client, aclInfo, portGroupName, aclNameIDs)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed adding ACL rules to port group %q for security ACL %q setup", portGroupName, aclName)
		}
	}

	// Add member ports to existing port groups.
	for _, aclName := range existingACLPortGroups {
		portGroupName := OVNACLPortGroupName(aclNameIDs[aclName])

		if reapplyRules {
			logger.Debug("Applying ACL rules to OVN port group", log.Ctx{"networkACL": aclName, "portGroup": portGroupName})
			_, aclInfo, err := s.Cluster.GetNetworkACL(aclProjectName, aclName)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed loading Network ACL %q", aclName)
			}

			err = ovnApplyToPortGroup(s, client, aclInfo, portGroupName, aclNameIDs)
			if err != nil {
				return nil, errors.Wrapf(err, "Failed applying ACL rules to port group %q for security ACL %q setup", portGroupName, aclName)
			}
		}

		if len(addMembers) > 0 {
			logger.Debug("Adding ACL OVN port group members", log.Ctx{"networkACL": aclName, "portGroup": portGroupName, "addMembers": addMembers})

			for _, memberName := range addMembers {
				err := client.PortGroupMemberAdd(portGroupName, memberUUIDs[memberName])
				if err != nil {
					return nil, errors.Wrapf(err, "Failed adding logical port %q to port group %q for security ACL %q setup", memberName, portGroupName, aclName)

				}
			}
		}
	}

	r := revert.Clone()
	revert.Success()
	return r, nil
}

// ovnAddReferencedACLs builds a recursive list of ACLs referenced by the rules in the supplied ACL.
func ovnAddReferencedACLs(s *state.State, aclProjectName string, info *api.NetworkACL, referencedACLNames map[string]*api.NetworkACL) error {
	recurseACLNames := []string{}

	addACLNamesFrom := func(ruleSubjects []string) error {
		for _, subject := range ruleSubjects {
			if validate.IsNetworkAddressCIDR(subject) == nil || validate.IsNetworkRange(subject) == nil {
				continue // Skip  if the subject is an IP CIDR or IP range.
			}

			// Anything else must be a referenced ACL name.
			if _, found := referencedACLNames[subject]; !found {
				_, aclInfo, err := s.Cluster.GetNetworkACL(aclProjectName, subject)
				if err != nil {
					return errors.Wrapf(err, "Failed loading Network ACL %q", subject)
				}

				referencedACLNames[subject] = aclInfo
				recurseACLNames = append(recurseACLNames, subject) // ACL to recurse into.
			}
		}

		return nil
	}

	for _, rule := range info.Ingress {
		err := addACLNamesFrom(util.SplitNTrimSpace(rule.Source, ",", -1, true))
		if err != nil {
			return err
		}
	}

	for _, rule := range info.Egress {
		err := addACLNamesFrom(util.SplitNTrimSpace(rule.Destination, ",", -1, true))
		if err != nil {
			return err
		}
	}

	// Recurse into referenced ACLs to build up full list.
	for _, recurseACLName := range recurseACLNames {
		if recurseACLName == info.Name {
			continue // Skip ourselves.
		}

		err := ovnAddReferencedACLs(s, aclProjectName, referencedACLNames[recurseACLName], referencedACLNames)
		if err != nil {
			return err
		}
	}

	return nil
}

// ovnApplyToPortGroup applies the rules in the specified ACL to the specified port group.
func ovnApplyToPortGroup(s *state.State, client *openvswitch.OVN, aclInfo *api.NetworkACL, portGroupName openvswitch.OVNPortGroup, aclNameIDs map[string]int64) error {
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
