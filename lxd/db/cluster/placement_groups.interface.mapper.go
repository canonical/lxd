//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// PlacementGroupGenerated is an interface of generated methods for PlacementGroup.
type PlacementGroupGenerated interface {
	// GetPlacementGroups returns all available placement_groups.
	// generator: placement_group GetMany
	GetPlacementGroups(ctx context.Context, tx *sql.Tx, filters ...PlacementGroupFilter) ([]PlacementGroup, error)

	// GetPlacementGroup returns the placement_group with the given key.
	// generator: placement_group GetOne
	GetPlacementGroup(ctx context.Context, tx *sql.Tx, name string, project string) (*PlacementGroup, error)

	// GetPlacementGroupID return the ID of the placement_group with the given key.
	// generator: placement_group ID
	GetPlacementGroupID(ctx context.Context, tx *sql.Tx, name string, project string) (int64, error)

	// PlacementGroupExists checks if a placement_group with the given key exists.
	// generator: placement_group Exists
	PlacementGroupExists(ctx context.Context, tx *sql.Tx, name string, project string) (bool, error)

	// CreatePlacementGroup adds a new placement_group to the database.
	// generator: placement_group Create
	CreatePlacementGroup(ctx context.Context, tx *sql.Tx, object PlacementGroup) (int64, error)

	// DeletePlacementGroup deletes the placement_group matching the given key parameters.
	// generator: placement_group DeleteOne-by-Name-and-Project
	DeletePlacementGroup(ctx context.Context, tx *sql.Tx, name string, project string) error

	// UpdatePlacementGroup updates the placement_group matching the given key parameters.
	// generator: placement_group Update
	UpdatePlacementGroup(ctx context.Context, tx *sql.Tx, name string, project string, object PlacementGroup) error

	// RenamePlacementGroup renames the placement_group matching the given key parameters.
	// generator: placement_group Rename
	RenamePlacementGroup(ctx context.Context, tx *sql.Tx, name string, project string, to string) error
}
