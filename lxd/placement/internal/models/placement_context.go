package models

import "github.com/canonical/lxd/shared/api"

// PlacementContext bundles every parameter any FilterStage might need, so a FilterStage's
// signature never grows bespoke arguments of its own — a FilterStage just reads the fields
// relevant to its own algorithm and ignores the rest. The engine that applies FilterStage values
// builds one PlacementContext per placement decision and sets only the fields the stages it runs
// actually use.
type PlacementContext struct {
	// Consulted by the placement-group stages (LoadPlacementGroupMembers,
	// FilterByClusterFailureDomains, FilterByPolicyAndRigor). PlacementGroup.Name == "" is the
	// sentinel for "no placement group" — a real placement group's name is never empty — so every
	// one of these stages is a no-op passthrough unless it's set.
	PlacementGroup        api.PlacementGroup
	Evacuation            bool
	ClusterFailureDomains []string

	// Consulted by FilterByClusterGroup. Only applies when PlacementGroup is unset — a placement
	// group's own scope/policy/rigor takes precedence over the coarser cluster-group membership
	// check.
	ClusterGroupName string

	// Populated by LoadPlacementGroupMembers; consulted by the placement-group stages that follow
	// it in the chain. Exported so lxd/placement/internal/filters, a separate package, can read
	// and write them.
	MemberToInst  map[int64][]int64
	MemberDomains MemberFailureDomains

	// Consulted by FilterByProjectFootprint. ProjectMaxHosts == 0 is the sentinel for "unset" — the
	// limits.max_hosts config key validator requires a value >= 1, so 0 can never be a legitimately
	// configured value — and FilterByProjectFootprint is a no-op passthrough when it's unset.
	ProjectName     string
	ProjectMaxHosts int
	ExcludeNodeID   *int64
}
