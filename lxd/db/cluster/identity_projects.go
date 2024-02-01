//go:build linux && cgo && !agent

package cluster

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t identity_projects.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e identity_project objects table=identities_projects
//go:generate mapper stmt -e identity_project objects-by-IdentityID table=identities_projects
//go:generate mapper stmt -e identity_project create struct=CertificateProject table=identities_projects
//go:generate mapper stmt -e identity_project delete-by-IdentityID table=identities_projects
//
//go:generate mapper method -i -e identity_project GetMany struct=Identity
//go:generate mapper method -i -e identity_project DeleteMany struct=Identity
//go:generate mapper method -i -e identity_project Create struct=Identity
//go:generate mapper method -i -e identity_project Update struct=Identity

// IdentityProject is an association table struct that associates
// identities to projects.
type IdentityProject struct {
	IdentityID int `db:"primary=yes"`
	ProjectID  int
}

// IdentityProjectFilter specifies potential query parameter fields.
type IdentityProjectFilter struct {
	IdentityID *int
	ProjectID  *int
}
