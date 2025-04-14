package api

// PlacementRuleset is a named collection of PlacementRule that may be applied to an instance or profile via
// the `placement.ruleset` configuration key.
//
// swagger:model
//
// API extension: instance_placement_rules.
type PlacementRuleset struct {
	WithEntitlements `yaml:",inline"`

	// Name is the name of the ruleset.
	//
	// Example: my-ha-deployment
	Name string `json:"name" yaml:"name"`

	// Project is the project containing the ruleset
	Project string `json:"project" yaml:"project"`

	// Description is a description of the ruleset.
	//
	// Example: Cluster member anti-affinity for instances with `user.ha=my-deployment`.
	Description string `json:"description" yaml:"description"`

	// PlacementRules is a map of rule name to PlacementRule. These are applied when placing instances configured with this ruleset.
	PlacementRules map[string]PlacementRule `json:"placement_rules" yaml:"placement_rules"`

	// UsedBy is a list of URLs of objects using this ruleset
	// Example: ["/1.0/instances/c1", "/1.0/profiles/default"]
	UsedBy []string `json:"used_by" yaml:"used_by"`
}

// Writable returns the writable (PUT/PATCH) fields of a PlacementRuleset.
func (r PlacementRuleset) Writable() PlacementRulesetPut {
	return PlacementRulesetPut{
		Description:    r.Description,
		PlacementRules: r.PlacementRules,
	}
}

// PlacementRulesetsPost contains the fields that may be specified on a PlacementRuleset on creation.
//
// swagger:model
//
// API extension: instance_placement_rules.
type PlacementRulesetsPost struct {
	PlacementRulesetPut `yaml:",inline"`

	// Name is the name of the ruleset.
	//
	// Example: my-ha-deployment
	Name string `json:"name" yaml:"name"`
}

// PlacementRulesetPut contains the fields that may be specified on a PlacementRuleset after creation.
//
// swagger:model
//
// API extension: instance_placement_rules.
type PlacementRulesetPut struct {
	// Description is a description of the ruleset.
	//
	// Example: Cluster member anti-affinity for instances with `user.ha=my-deployment`.
	Description string `json:"description" yaml:"description"`

	// PlacementRules is a map of rule name to PlacementRule. These are applied when placing instances configured with this ruleset.
	PlacementRules map[string]PlacementRule `json:"placement_rules" yaml:"placement_rules"`
}

// PlacementRulesetPost contains a single Name field, for renaming a PlacementRuleset.
//
// swagger:model
//
// API extension: instance_placement_rules.
type PlacementRulesetPost struct {
	// Name is the new name of the ruleset.
	//
	// Example: my-ha-deployment
	Name string `json:"name" yaml:"name"`
}

// PlacementRuleKind denotes the placement strategy for a PlacementRule.
//
// API extension: instance_placement_rules.
type PlacementRuleKind string

const (
	// PlacementRuleKindMemberAffinity indicates that an instance should be placed in or with entities that are found by associated selectors.
	PlacementRuleKindMemberAffinity PlacementRuleKind = "member-affinity"

	// PlacementRuleKindMemberAntiAffinity indicates that an instance should be placed away from or not with entities that are found by associated selectors.
	PlacementRuleKindMemberAntiAffinity PlacementRuleKind = "member-anti-affinity"
)

// PlacementRule represents a scheduling rule for an instance.
//
// swagger:model
//
// API extension: instance_placement_rules.
type PlacementRule struct {
	// Required indicates that placement should fail if the rule cannot be satisfied.
	//
	// Example: true
	Required bool `json:"required" yaml:"required"`

	// Kind indicates how to apply the rule.
	//
	// Example: affinity
	Kind PlacementRuleKind `json:"kind" yaml:"kind"`

	// Priority indicates the apply order of the rule if it is not required.
	// Non-required rules are applied in descending order of priority.
	//
	// Example: 1
	Priority int `json:"priority" yaml:"priority"`

	// Selector determines the entities that the rule is applied against.
	Selector Selector `json:"selector" yaml:"selector"`
}
