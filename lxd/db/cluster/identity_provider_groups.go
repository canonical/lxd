package cluster

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t identity_provider_groups.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e identity_provider_group objects table=identity_provider_groups
//go:generate mapper stmt -e identity_provider_group objects-by-ID table=identity_provider_groups
//go:generate mapper stmt -e identity_provider_group objects-by-Name table=identity_provider_groups
//go:generate mapper stmt -e identity_provider_group id table=identity_provider_groups
//go:generate mapper stmt -e identity_provider_group create table=identity_provider_groups
//go:generate mapper stmt -e identity_provider_group delete-by-Name table=identity_provider_groups
//go:generate mapper stmt -e identity_provider_group update table=identity_provider_groups
//go:generate mapper stmt -e identity_provider_group rename table=identity_provider_groups
//
//go:generate mapper method -i -e identity_provider_group GetMany
//go:generate mapper method -i -e identity_provider_group GetOne
//go:generate mapper method -i -e identity_provider_group ID
//go:generate mapper method -i -e identity_provider_group Exists
//go:generate mapper method -i -e identity_provider_group Create
//go:generate mapper method -i -e identity_provider_group DeleteOne-by-Name
//go:generate mapper method -i -e identity_provider_group Update
//go:generate mapper method -i -e identity_provider_group Rename

// IdentityProviderGroup is the database representation of an api.IdentityProviderGroup.
type IdentityProviderGroup struct {
	ID   int
	Name string `db:"primary=true"`
}

// IdentityProviderGroupFilter contains the columns that a queries for identity provider groups can be filtered upon.
type IdentityProviderGroupFilter struct {
	ID   *int
	Name *string
}
