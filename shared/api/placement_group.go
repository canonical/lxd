package api

const (
	// PlacementPolicySpread spreads instances across cluster members.
	PlacementPolicySpread string = "spread"

	// PlacementPolicyCompact colocates instances on the same cluster member.
	PlacementPolicyCompact string = "compact"
)

const (
	// PlacementRigorStrict enforces the placement policy strictly.
	// Placement fails if the requested policy cannot be satisfied.
	PlacementRigorStrict string = "strict"

	// PlacementRigorPermissive relaxes enforcement of the placement policy.
	// Placement continues even if the ideal policy cannot be fully met.
	PlacementRigorPermissive string = "permissive"
)

// PlacementGroup represents a group of instances that should be scheduled.
//
// API extension: instance_placement_groups.
type PlacementGroup struct {
	WithEntitlements `yaml:",inline"`

	// Name of the placement group.
	// Example: pg1
	Name string `json:"name" yaml:"name"`

	// Description of the placement group.
	// Example: My placement group.
	Description string `json:"description" yaml:"description"`

	// Placement group configuration map (refer to doc/placement-groups.md)
	// Example: {"user.mykey": "foo", "policy: "compact", "rigor": "permissive"}
	Config map[string]string `json:"config" yaml:"config"`

	// Project the placement group belongs to.
	// Example: default
	Project string `json:"project" yaml:"project"`

	// List of URLs of objects using this placement group.
	// Example: ["/1.0/instances/c1", "/1.0/profiles/default"]
	UsedBy []string `json:"used_by" yaml:"used_by"`
}

// PlacementGroupsPost represents the fields required to create a new placement group.
//
// API extension: instance_placement_groups.
type PlacementGroupsPost struct {
	// Name of the placement group.
	// Example: pg1
	Name string `json:"name" yaml:"name"`

	PlacementGroupPut `yaml:",inline"`
}

// PlacementGroupPut represents the modifiable fields of a placement group.
//
// API extension: instance_placement_groups.
type PlacementGroupPut struct {
	// Description of the placement group.
	// Example: My placement group.
	Description string `json:"description" yaml:"description"`

	// Placement group configuration map (refer to doc/placement-groups.md)
	// Example: {"user.mykey": "foo", "policy: "spread", "rigor": "strict"}
	Config map[string]string `json:"config" yaml:"config"`
}

// Writable returns the editable fields of a [PlacementGroup] as [PlacementGroupPut].
func (p PlacementGroup) Writable() PlacementGroupPut {
	return PlacementGroupPut{
		Description: p.Description,
		Config:      p.Config,
	}
}

// PlacementGroupPost represents the fields required to rename a placement group.
//
// API extension: instance_placement_groups.
type PlacementGroupPost struct {
	// New name of the placement group.
	// Example: pg2
	Name string `json:"name" yaml:"name"`
}
