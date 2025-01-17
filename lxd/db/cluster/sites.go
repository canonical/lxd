package cluster

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t sites.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e site objects table=sites
//go:generate mapper stmt -e site objects-by-ID table=sites
//go:generate mapper stmt -e site objects-by-Name table=sites
//go:generate mapper stmt -e site id table=sites
//go:generate mapper stmt -e site create table=sites
//go:generate mapper stmt -e site delete-by-Name table=sites
//go:generate mapper stmt -e site update table=sites
//go:generate mapper stmt -e site rename table=sites
//
//go:generate mapper method -i -e site GetMany references=Config
//go:generate mapper method -i -e site GetOne
//go:generate mapper method -i -e site ID
//go:generate mapper method -i -e site Exists
//go:generate mapper method -i -e site Create references=Config
//go:generate mapper method -i -e site DeleteOne-by-Name
//go:generate mapper method -i -e site Update references=Config
//go:generate mapper method -i -e site Rename
//go:generate goimports -w sites.mapper.go
//go:generate goimports -w sites.interface.mapper.go

// Site is the database representation of an api.Site.
type Site struct {
	ID          int
	IdentityID  int    `db:"omit=create,update"`
	Name        string `db:"primary=true"`
	Addresses   string
	Type        int
	Delegated   int
	Description string `db:"coalesce=''"`
}

// SiteFilter contains fields upon which a site can be filtered.
type SiteFilter struct {
	ID   *int
	Name *string
}
