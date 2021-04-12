package acl

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/cluster/request"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/network/openvswitch"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
	"github.com/lxc/lxd/shared/validate"
	"github.com/lxc/lxd/shared/version"
)

// Define type for rule directions.
type ruleDirection string

const ruleDirectionIngress ruleDirection = "ingress"
const ruleDirectionEgress ruleDirection = "egress"

// Define reserved ACL subjects.
const ruleSubjectInternal = "@internal"
const ruleSubjectExternal = "@external"

// Define aliases for reserved ACL subjects. This is to allow earlier deprecated names that used the "#" prefix.
// They were deprecated to avoid confusion with YAML comments. So "#internal" and "#external" should not be used.
var ruleSubjectInternalAliases = []string{ruleSubjectInternal, "#internal"}
var ruleSubjectExternalAliases = []string{ruleSubjectExternal, "#external"}

// ValidActions defines valid actions for rules.
var ValidActions = []string{"allow", "drop", "reject"}

// common represents a Network ACL.
type common struct {
	logger      logger.Logger
	state       *state.State
	id          int64
	projectName string
	info        *api.NetworkACL
}

// init initialise internal variables.
func (d *common) init(state *state.State, id int64, projectName string, info *api.NetworkACL) {
	if info == nil {
		d.info = &api.NetworkACL{}
	} else {
		d.info = info
	}

	d.logger = logging.AddContext(logger.Log, log.Ctx{"project": projectName, "networkACL": d.info.Name})
	d.id = id
	d.projectName = projectName
	d.state = state

	if d.info.Ingress == nil {
		d.info.Ingress = []api.NetworkACLRule{}
	}

	for i := range d.info.Ingress {
		d.info.Ingress[i].Normalise()
	}

	if d.info.Egress == nil {
		d.info.Egress = []api.NetworkACLRule{}
	}

	for i := range d.info.Egress {
		d.info.Egress[i].Normalise()
	}

	if d.info.Config == nil {
		d.info.Config = make(map[string]string)
	}
}

// ID returns the Network ACL ID.
func (d *common) ID() int64 {
	return d.id
}

// Name returns the project.
func (d *common) Project() string {
	return d.projectName
}

// Info returns copy of internal info for the Network ACL.
func (d *common) Info() *api.NetworkACL {
	// Copy internal info to prevent modification externally.
	info := api.NetworkACL{}
	info.Name = d.info.Name
	info.Description = d.info.Description
	info.Ingress = append(make([]api.NetworkACLRule, 0, len(d.info.Ingress)), d.info.Ingress...)
	info.Egress = append(make([]api.NetworkACLRule, 0, len(d.info.Egress)), d.info.Egress...)
	info.Config = util.CopyConfig(d.info.Config)
	info.UsedBy = nil // To indicate its not populated (use Usedby() function to populate).

	return &info
}

// usedBy returns a list of API endpoints referencing this ACL.
// If firstOnly is true then search stops at first result.
func (d *common) usedBy(firstOnly bool) ([]string, error) {
	usedBy := []string{}

	// Find all networks, profiles and instance NICs that use this Network ACL.
	err := UsedBy(d.state, d.projectName, func(_ []string, usageType interface{}, _ string, _ map[string]string) error {
		switch u := usageType.(type) {
		case db.Instance:
			uri := fmt.Sprintf("/%s/instances/%s", version.APIVersion, u.Name)
			if u.Project != project.Default {
				uri += fmt.Sprintf("?project=%s", u.Project)
			}

			usedBy = append(usedBy, uri)
		case *api.Network:
			uri := fmt.Sprintf("/%s/networks/%s", version.APIVersion, u.Name)
			if d.projectName != project.Default {
				uri += fmt.Sprintf("?project=%s", d.projectName)
			}

			usedBy = append(usedBy, uri)
		case db.Profile:
			uri := fmt.Sprintf("/%s/profiles/%s", version.APIVersion, u.Name)
			if u.Project != project.Default {
				uri += fmt.Sprintf("?project=%s", u.Project)
			}

			usedBy = append(usedBy, uri)
		case *api.NetworkACL:
			uri := fmt.Sprintf("/%s/network-acls/%s", version.APIVersion, u.Name)
			if d.projectName != project.Default {
				uri += fmt.Sprintf("?project=%s", d.projectName)
			}

			usedBy = append(usedBy, uri)
		default:
			return fmt.Errorf("Unrecognised usage type %T", u)
		}

		if firstOnly {
			return db.ErrInstanceListStop
		}

		return nil
	}, d.Info().Name)
	if err != nil {
		if err == db.ErrInstanceListStop {
			return usedBy, nil
		}

		return nil, errors.Wrapf(err, "Failed getting ACL usage")
	}

	return usedBy, nil
}

// UsedBy returns a list of API endpoints referencing this ACL.
func (d *common) UsedBy() ([]string, error) {
	return d.usedBy(false)
}

// isUsed returns whether or not the ACL is in use.
func (d *common) isUsed() (bool, error) {
	usedBy, err := d.usedBy(true)
	if err != nil {
		return false, err
	}

	return len(usedBy) > 0, nil
}

// Etag returns the values used for etag generation.
func (d *common) Etag() []interface{} {
	return []interface{}{d.info.Name, d.info.Description, d.info.Ingress, d.info.Egress, d.info.Config}
}

// validateName checks name is valid.
func (d *common) validateName(name string) error {
	if name == "" {
		return fmt.Errorf("Name is required")
	}

	// Don't allow ACL names to start with special port selector characters to allow LXD to define special port
	// selectors without risking conflict with user defined ACL names.
	if shared.StringHasPrefix(name, "@", "%", "#") {
		return fmt.Errorf("Name cannot start with reserved character %q", name[0])
	}

	// Ensures we can differentiate an ACL name from an IP in rules that reference this ACL.
	err := shared.ValidHostname(name)
	if err != nil {
		return err
	}

	return nil
}

// validateConfig checks the config and rules are valid.
func (d *common) validateConfig(info *api.NetworkACLPut) error {
	err := d.validateConfigMap(info.Config, nil)
	if err != nil {
		return err
	}

	// Normalise rules before validation for duplicate detection.
	for i := range info.Ingress {
		info.Ingress[i].Normalise()
	}

	for i := range info.Egress {
		info.Egress[i].Normalise()
	}

	// Validate each ingress rule.
	for i, ingressRule := range info.Ingress {
		err := d.validateRule(ruleDirectionIngress, ingressRule)
		if err != nil {
			return errors.Wrapf(err, "Invalid ingress rule %d", i)
		}

		// Check for duplicates.
		for ri, r := range info.Ingress {
			if ri == i {
				continue // Skip ourselves.
			}

			if r == ingressRule {
				return fmt.Errorf("Duplicate of ingress rule %d", i)
			}
		}
	}

	// Validate each egress rule.
	for i, egressRule := range info.Egress {
		err := d.validateRule(ruleDirectionEgress, egressRule)
		if err != nil {
			return errors.Wrapf(err, "Invalid egress rule %d", i)
		}

		// Check for duplicates.
		for ri, r := range info.Egress {
			if ri == i {
				continue // Skip ourselves.
			}

			if r == egressRule {
				return fmt.Errorf("Duplicate of egress rule %d", i)
			}
		}
	}

	return nil
}

// validateConfigMap checks ACL config map against rules.
func (d *common) validateConfigMap(config map[string]string, rules map[string]func(value string) error) error {
	checkedFields := map[string]struct{}{}

	// Run the validator against each field.
	for k, validator := range rules {
		checkedFields[k] = struct{}{} //Mark field as checked.
		err := validator(config[k])
		if err != nil {
			return errors.Wrapf(err, "Invalid value for config option %q", k)
		}
	}

	// Look for any unchecked fields, as these are unknown fields and validation should fail.
	for k := range config {
		_, checked := checkedFields[k]
		if checked {
			continue
		}

		// User keys are not validated.
		if shared.IsUserConfig(k) {
			continue
		}

		return fmt.Errorf("Invalid config option %q", k)
	}

	return nil
}

// validateRule validates the rule supplied.
func (d *common) validateRule(direction ruleDirection, rule api.NetworkACLRule) error {
	// Validate Action field (required).
	if !shared.StringInSlice(rule.Action, ValidActions) {
		return fmt.Errorf("Action must be one of: %s", strings.Join(ValidActions, ", "))
	}

	// Validate State field (required).
	validStates := []string{"enabled", "disabled", "logged"}
	if !shared.StringInSlice(rule.State, validStates) {
		return fmt.Errorf("State must be one of: %s", strings.Join(validStates, ", "))
	}

	// Get map of ACL names to DB IDs (used for generating OVN port group names).
	acls, err := d.state.Cluster.GetNetworkACLIDsByNames(d.Project())
	if err != nil {
		return errors.Wrapf(err, "Failed getting network ACLs for security ACL subject validation")
	}

	allowedSubjectNames := make([]string, 0, len(acls)+2)
	allowedSubjectNames = append(allowedSubjectNames, ruleSubjectInternalAliases...)
	allowedSubjectNames = append(allowedSubjectNames, ruleSubjectExternalAliases...)

	for aclName := range acls {
		allowedSubjectNames = append(allowedSubjectNames, aclName)
	}

	// Validate Source field.
	if rule.Source != "" {
		var validSubjects []string
		if direction == ruleDirectionIngress {
			validSubjects = allowedSubjectNames // Names are only allowed in ingress rule sources.
		}

		err := d.validateRuleSubjects(util.SplitNTrimSpace(rule.Source, ",", -1, false), validSubjects)
		if err != nil {
			return errors.Wrapf(err, "Invalid Source")
		}
	}

	// Validate Destination field.
	if rule.Destination != "" {
		var validSubjects []string
		if direction == ruleDirectionEgress {
			validSubjects = allowedSubjectNames // Names are only allowed in egress rule destinations.
		}

		err := d.validateRuleSubjects(util.SplitNTrimSpace(rule.Destination, ",", -1, false), validSubjects)
		if err != nil {
			return errors.Wrapf(err, "Invalid Destination")
		}
	}

	// Validate Protocol field.
	if rule.Protocol != "" {
		validProtocols := []string{"icmp4", "icmp6", "tcp", "udp"}
		if !shared.StringInSlice(rule.Protocol, validProtocols) {
			return fmt.Errorf("Protocol must be one of: %s", strings.Join(validProtocols, ", "))
		}
	}

	// Validate protocol dependent fields.
	if shared.StringInSlice(rule.Protocol, []string{"tcp", "udp"}) {
		if rule.ICMPType != "" {
			return fmt.Errorf("ICMP type cannot be used with protocol")
		}

		if rule.ICMPCode != "" {
			return fmt.Errorf("ICMP code cannot be used with protocol")
		}

		// Validate SourcePort field.
		if rule.SourcePort != "" {
			err := d.validatePorts(util.SplitNTrimSpace(rule.SourcePort, ",", -1, false))
			if err != nil {
				return errors.Wrapf(err, "Invalid Source port")
			}
		}

		// Validate DestinationPort field.
		if rule.DestinationPort != "" {
			err := d.validatePorts(util.SplitNTrimSpace(rule.DestinationPort, ",", -1, false))
			if err != nil {
				return errors.Wrapf(err, "Invalid Destination port")
			}
		}
	} else if shared.StringInSlice(rule.Protocol, []string{"icmp4", "icmp6"}) {
		if rule.SourcePort != "" {
			return fmt.Errorf("Source port cannot be used with protocol")
		}

		if rule.DestinationPort != "" {
			return fmt.Errorf("Destination port cannot be used with protocol")
		}

		// Validate ICMPType field.
		if rule.ICMPType != "" {
			err := validate.IsUint8(rule.ICMPType)
			if err != nil {
				return errors.Wrapf(err, "Invalid ICMP type")
			}
		}

		// Validate ICMPCode field.
		if rule.ICMPCode != "" {
			err := validate.IsUint8(rule.ICMPCode)
			if err != nil {
				return errors.Wrapf(err, "Invalid ICMP code")
			}
		}
	} else {
		if rule.ICMPType != "" {
			return fmt.Errorf("ICMP type cannot be used without specifying protocol")
		}

		if rule.ICMPCode != "" {
			return fmt.Errorf("ICMP code cannot be used without specifying protocol")
		}

		if rule.SourcePort != "" {
			return fmt.Errorf("Source port cannot be used without specifying protocol")
		}

		if rule.DestinationPort != "" {
			return fmt.Errorf("Destination port cannot be used without specifying protocol")
		}
	}

	return nil
}

// validateRuleSubjects checks that the source or destination subjects for a rule are valid.
// Accepts an allowedNames list of allowed ACL or special classifier names.
func (d *common) validateRuleSubjects(subjects []string, allowedNames []string) error {
	checks := []func(s string) error{
		validate.IsNetworkAddressCIDR,
		validate.IsNetworkRange,
	}

	validSubject := func(subject string, allowedNames []string) error {
		// Check if it is one of the network IP types.
		for _, c := range checks {
			err := c(subject)
			if err == nil {
				return nil // Found valid subject.

			}
		}

		// Check if it is one of the allowed names.
		for _, n := range allowedNames {
			if subject == n {
				return nil // Found valid subject.
			}
		}

		return fmt.Errorf("Invalid subject %q", subject)
	}

	for _, s := range subjects {
		err := validSubject(s, allowedNames)
		if err != nil {
			return err
		}
	}

	return nil
}

// validatePorts checks that the source or destination ports for a rule are valid.
func (d *common) validatePorts(ports []string) error {
	checks := []func(s string) error{
		validate.IsNetworkPort,
		validate.IsNetworkPortRange,
	}

	validPort := func(port string) error {
		// Check if it is one of the network port types.
		for _, c := range checks {
			err := c(port)
			if err == nil {
				return nil // Found valid port.

			}
		}

		return fmt.Errorf("Invalid port %q", port)
	}

	for _, port := range ports {
		err := validPort(port)
		if err != nil {
			return err
		}
	}

	return nil
}

// Update applies the supplied config to the ACL.
func (d *common) Update(config *api.NetworkACLPut, clientType request.ClientType) error {
	err := d.validateConfig(config)
	if err != nil {
		return err
	}

	revert := revert.New()
	defer revert.Fail()

	if clientType == request.ClientTypeNormal {
		oldConfig := d.info.NetworkACLPut

		// Update database. Its important this occurs before we attempt to apply to networks using the ACL
		// as usage functions will inspect the database.
		err = d.state.Cluster.UpdateNetworkACL(d.id, config)
		if err != nil {
			return err
		}

		// Apply changes internally and reinitialise.
		d.info.NetworkACLPut = *config
		d.init(d.state, d.id, d.projectName, d.info)

		revert.Add(func() {
			d.state.Cluster.UpdateNetworkACL(d.id, &oldConfig)
			d.info.NetworkACLPut = oldConfig
			d.init(d.state, d.id, d.projectName, d.info)
		})
	}

	// Get a list of networks that are using this ACL (either directly or indirectly via a NIC).
	aclNets := map[string]NetworkACLUsage{}
	err = NetworkUsage(d.state, d.projectName, []string{d.info.Name}, aclNets)
	if err != nil {
		return errors.Wrapf(err, "Failed getting ACL network usage")
	}

	// Separate out OVN networks from non-OVN networks. This is because OVN networks share ACL config, and
	// so changes are not applied entirely on a per-network basis and need to be treated differently.
	aclOVNNets := map[string]NetworkACLUsage{}
	for k, v := range aclNets {
		if v.Type == "ovn" {
			delete(aclNets, k)
			aclOVNNets[k] = v
		} else if v.Type != "bridge" {
			return fmt.Errorf("Unsupported network ACL type %q", v.Type)
		}
	}

	// Apply ACL changes to non-OVN networks on this member.
	for _, aclNet := range aclNets {
		err = FirewallApplyACLRules(d.state, d.logger, d.projectName, aclNet)
		if err != nil {
			return err
		}
	}

	// If there are affected OVN networks, then apply the changes, but only if the request type is normal.
	// This way we won't apply the same changes multiple times for each LXD cluster member.
	if len(aclOVNNets) > 0 && clientType == request.ClientTypeNormal {
		client, err := openvswitch.NewOVN(d.state)
		if err != nil {
			return errors.Wrapf(err, "Failed to get OVN client")
		}

		// Get map of ACL names to DB IDs (used for generating OVN port group names).
		aclNameIDs, err := d.state.Cluster.GetNetworkACLIDsByNames(d.Project())
		if err != nil {
			return errors.Wrapf(err, "Failed getting network ACL IDs for security ACL update")
		}

		// Request that the ACL and any referenced ACLs in the ruleset are created in OVN.
		// Pass aclOVNNets info, because although OVN networks share ACL port group definitions, when the
		// ACL rules themselves use network specific selectors such as @internal/@external, we then need to
		// apply those rules to each network affected by the ACL, so pass the full list of OVN networks
		// affected by this ACL (either because the ACL is assigned directly or because it is assigned to
		// an OVN NIC in an instance or profile).
		r, err := OVNEnsureACLs(d.state, d.logger, client, d.projectName, aclNameIDs, aclOVNNets, []string{d.info.Name}, true)
		if err != nil {
			return errors.Wrapf(err, "Failed ensuring ACL is configured in OVN")
		}
		revert.Add(r.Fail)

		// Run unused port group cleanup in case any formerly referenced ACL in this ACL's rules means that
		// an ACL port group is now considered unused.
		err = OVNPortGroupDeleteIfUnused(d.state, d.logger, client, d.projectName, nil, "", d.info.Name)
		if err != nil {
			return errors.Wrapf(err, "Failed removing unused OVN port groups")
		}
	}

	// Apply ACL changes to non-OVN networks on cluster members.
	if clientType == request.ClientTypeNormal && len(aclNets) > 0 {
		// Notify all other nodes to update the network if no target specified.
		notifier, err := cluster.NewNotifier(d.state, d.state.Endpoints.NetworkCert(), cluster.NotifyAll)
		if err != nil {
			return err
		}

		err = notifier(func(client lxd.InstanceServer) error {
			return client.UseProject(d.projectName).UpdateNetworkACL(d.info.Name, d.info.NetworkACLPut, "")
		})
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// Rename renames the ACL if not in use.
func (d *common) Rename(newName string) error {
	_, err := LoadByName(d.state, d.projectName, newName)
	if err == nil {
		return fmt.Errorf("An ACL by that name exists already")
	}

	isUsed, err := d.isUsed()
	if err != nil {
		return err
	}

	if isUsed {
		return fmt.Errorf("Cannot rename an ACL that is in use")
	}

	err = d.validateName(newName)
	if err != nil {
		return err
	}

	err = d.state.Cluster.RenameNetworkACL(d.id, newName)
	if err != nil {
		return err
	}

	// Apply changes internally.
	d.info.Name = newName

	return nil
}

// Delete deletes the ACL.
func (d *common) Delete() error {
	isUsed, err := d.isUsed()
	if err != nil {
		return err
	}

	if isUsed {
		return fmt.Errorf("Cannot delete an ACL that is in use")
	}

	return d.state.Cluster.DeleteNetworkACL(d.id)
}
