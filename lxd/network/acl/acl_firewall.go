package acl

import (
	"fmt"

	"github.com/pkg/errors"

	firewallDrivers "github.com/lxc/lxd/lxd/firewall/drivers"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

// FirewallApplyACLRules applies ACL rules to network firewall.
func FirewallApplyACLRules(s *state.State, logger logger.Logger, aclProjectName string, aclNet NetworkACLUsage) error {
	var dropRules []firewallDrivers.ACLRule
	var rejectRules []firewallDrivers.ACLRule
	var allowRules []firewallDrivers.ACLRule

	// convertACLRules converts the ACL rules to Firewall ACL rules.
	convertACLRules := func(direction string, logPrefix string, rules ...api.NetworkACLRule) error {
		for ruleIndex, rule := range rules {
			if rule.State == "disabled" {
				continue
			}

			firewallACLRule := firewallDrivers.ACLRule{
				Direction:       direction,
				Action:          rule.Action,
				Source:          rule.Source,
				Destination:     rule.Destination,
				Protocol:        rule.Protocol,
				SourcePort:      rule.SourcePort,
				DestinationPort: rule.DestinationPort,
				ICMPType:        rule.ICMPType,
				ICMPCode:        rule.ICMPCode,
			}

			if rule.State == "logged" {
				firewallACLRule.Log = true
				// Max 29 chars.
				firewallACLRule.LogName = fmt.Sprintf("%s-%s-%d", logPrefix, direction, ruleIndex)
			}

			switch {
			case rule.Action == "drop":
				dropRules = append(dropRules, firewallACLRule)
			case rule.Action == "reject":
				rejectRules = append(rejectRules, firewallACLRule)
			case rule.Action == "allow":
				allowRules = append(allowRules, firewallACLRule)
			default:
				return fmt.Errorf("Unrecognised action %q", rule.Action)
			}
		}

		return nil
	}

	logPrefix := aclNet.Name

	// Load ACLs specified by network.
	for _, aclName := range util.SplitNTrimSpace(aclNet.Config["security.acls"], ",", -1, true) {
		_, aclInfo, err := s.Cluster.GetNetworkACL(aclProjectName, aclName)
		if err != nil {
			return errors.Wrapf(err, "Failed loading ACL %q for network %q", aclName, aclNet.Name)
		}

		err = convertACLRules("ingress", logPrefix, aclInfo.Ingress...)
		if err != nil {
			return errors.Wrapf(err, "Failed converting ACL %q ingress rules for network %q", aclInfo.Name, aclNet.Name)
		}

		err = convertACLRules("egress", logPrefix, aclInfo.Egress...)
		if err != nil {
			return errors.Wrapf(err, "Failed converting ACL %q egress rules for network %q", aclInfo.Name, aclNet.Name)
		}
	}

	var rules []firewallDrivers.ACLRule
	rules = append(rules, dropRules...)
	rules = append(rules, rejectRules...)
	rules = append(rules, allowRules...)

	// Add the automatic default ACL rule for the network.
	egressAction, egressLogged := firewallACLDefaults(aclNet.Config, "egress")
	ingressAction, ingressLogged := firewallACLDefaults(aclNet.Config, "ingress")

	rules = append(rules, firewallDrivers.ACLRule{
		Direction: "egress",
		Action:    egressAction,
		Log:       egressLogged,
		LogName:   fmt.Sprintf("%s-egress", logPrefix),
	})

	rules = append(rules, firewallDrivers.ACLRule{
		Direction: "ingress",
		Action:    ingressAction,
		Log:       ingressLogged,
		LogName:   fmt.Sprintf("%s-ingress", logPrefix),
	})

	return s.Firewall.NetworkApplyACLRules(aclNet.Name, rules)
}

// firewallACLDefaults returns the action and logging mode to use for the specified direction's default rule.
// If the security.acls.default.{in,e}gress.action or security.acls.default.{in,e}gress.logged settings are not
// specified in the network config, then it returns "reject" and false respectively.
func firewallACLDefaults(netConfig map[string]string, direction string) (string, bool) {
	defaults := map[string]string{
		fmt.Sprintf("security.acls.default.%s.action", direction): "reject",
		fmt.Sprintf("security.acls.default.%s.logged", direction): "false",
	}

	for k := range defaults {
		if netConfig[k] != "" {
			defaults[k] = netConfig[k]
		}
	}

	return defaults[fmt.Sprintf("security.acls.default.%s.action", direction)], shared.IsTrue(defaults[fmt.Sprintf("security.acls.default.%s.logged", direction)])
}
