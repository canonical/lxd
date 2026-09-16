package models

import "github.com/canonical/lxd/shared/api"

// PlacementContext bundles every parameter any FilterStage might need, so a FilterStage's
// signature never grows bespoke arguments of its own — a FilterStage just reads the fields
// relevant to its own algorithm and ignores the rest. The engine that applies FilterStage values
// builds one PlacementContext per placement decision and sets only the fields the stages it runs
// actually use.
type PlacementContext struct {
	// Consulted by FilterByPlacementGroup. PlacementGroup.Name == "" is the sentinel for "no
	// placement group" — a real placement group's name is never empty — so FilterByPlacementGroup
	// is a no-op passthrough unless it's set.
	PlacementGroup api.PlacementGroup
	Evacuation     bool

	// Consulted by FilterByClusterGroup. Only applies when PlacementGroup is unset — a placement
	// group's own scope/policy/rigor takes precedence over the coarser cluster-group membership
	// check.
	ClusterGroupName string
}
