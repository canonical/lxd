package models

import "github.com/canonical/lxd/shared/api"

// PlacementContext bundles every parameter any FilterStage might need, so a FilterStage's
// signature never grows bespoke arguments of its own — a FilterStage just reads the fields
// relevant to its own algorithm and ignores the rest. The engine that applies FilterStage values
// builds one PlacementContext per placement decision and sets only the fields the stages it runs
// actually use.
type PlacementContext struct {
	// Consulted by FilterByPlacementGroup.
	PlacementGroup api.PlacementGroup
	Evacuation     bool
}
