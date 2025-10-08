package cluster

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t placement_groups.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e placement_group objects table=placement_groups
//go:generate mapper stmt -e placement_group objects-by-ID table=placement_groups
//go:generate mapper stmt -e placement_group objects-by-Project table=placement_groups
//go:generate mapper stmt -e placement_group objects-by-Name-and-Project table=placement_groups
//go:generate mapper stmt -e placement_group id table=placement_groups
//go:generate mapper stmt -e placement_group create struct=PlacementGroup table=placement_groups
//go:generate mapper stmt -e placement_group delete-by-Name-and-Project table=placement_groups
//go:generate mapper stmt -e placement_group update struct=PlacementGroup table=placement_groups
//go:generate mapper stmt -e placement_group rename struct=PlacementGroup table=placement_groups
//
//go:generate mapper method -i -e placement_group GetMany
//go:generate mapper method -i -e placement_group GetOne
//go:generate mapper method -i -e placement_group ID struct=PlacementGroup
//go:generate mapper method -i -e placement_group Exists struct=PlacementGroup
//go:generate mapper method -i -e placement_group Create struct=PlacementGroup
//go:generate mapper method -i -e placement_group DeleteOne-by-Name-and-Project
//go:generate mapper method -i -e placement_group Update struct=PlacementGroup
//go:generate mapper method -i -e placement_group Rename struct=PlacementGroup
//go:generate goimports -w placement_groups.mapper.go
//go:generate goimports -w placement_groups.interface.mapper.go

// PlacementGroup is the database representation of an [api.PlacementGroup].
type PlacementGroup struct {
	ID          int
	Name        string `db:"primary=yes"`
	Project     string `db:"primary=yes&join=projects.name"`
	Description string `db:"coalesce=''"`
}

// PlacementGroupFilter contains fields that can be used to filter results when getting placement groups.
type PlacementGroupFilter struct {
	ID              *int
	Project         *string
	Name            *string
	ExcludeMemberID *int64
}
