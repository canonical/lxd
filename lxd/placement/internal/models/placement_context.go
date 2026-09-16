package models

import (
	"github.com/canonical/lxd/shared/api"
)

// PlacementContext bundles every parameter any FilterStage might need. The engine builds one
// per placement decision and sets only the fields the stages it runs actually use.
type PlacementContext struct {
	// Consulted by FilterByPlacementGroup.
	PlacementGroup api.PlacementGroup
	Evacuation     bool
}
