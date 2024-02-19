package cluster

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t auth_groups.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e auth_group objects table=auth_groups
//go:generate mapper stmt -e auth_group objects-by-ID table=auth_groups
//go:generate mapper stmt -e auth_group objects-by-Name table=auth_groups
//go:generate mapper stmt -e auth_group id table=auth_groups
//go:generate mapper stmt -e auth_group create table=auth_groups
//go:generate mapper stmt -e auth_group delete-by-Name table=auth_groups
//go:generate mapper stmt -e auth_group update table=auth_groups
//go:generate mapper stmt -e auth_group rename table=auth_groups
//
//go:generate mapper method -i -e auth_group GetMany
//go:generate mapper method -i -e auth_group GetOne
//go:generate mapper method -i -e auth_group ID
//go:generate mapper method -i -e auth_group Exists
//go:generate mapper method -i -e auth_group Create
//go:generate mapper method -i -e auth_group DeleteOne-by-Name
//go:generate mapper method -i -e auth_group Update
//go:generate mapper method -i -e auth_group Rename

// AuthGroup is the database representation of an api.AuthGroup.
type AuthGroup struct {
	ID          int
	Name        string `db:"primary=true"`
	Description string
}

// AuthGroupFilter contains fields upon which an AuthGroup can be filtered.
type AuthGroupFilter struct {
	ID   *int
	Name *string
}
