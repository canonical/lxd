package cluster

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t services.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e service objects table=services
//go:generate mapper stmt -e service objects-by-ID table=services
//go:generate mapper stmt -e service objects-by-Name table=services
//go:generate mapper stmt -e service id table=services
//go:generate mapper stmt -e service create table=services
//go:generate mapper stmt -e service delete-by-Name table=services
//go:generate mapper stmt -e service update table=services
//go:generate mapper stmt -e service rename table=services
//
//go:generate mapper method -i -e service GetMany references=Config
//go:generate mapper method -i -e service GetOne
//go:generate mapper method -i -e service ID
//go:generate mapper method -i -e service Exists
//go:generate mapper method -i -e service Create references=Config
//go:generate mapper method -i -e service DeleteOne-by-Name
//go:generate mapper method -i -e service Update references=Config
//go:generate mapper method -i -e service Rename
//go:generate goimports -w services.mapper.go
//go:generate goimports -w services.interface.mapper.go

// Service is the database representation of an api.Service.
type Service struct {
	ID          int
	IdentityID  int
	Name        string `db:"primary=true"`
	Addresses   string
	Type        int
	Description string `db:"coalesce=''"`
}

// ServiceFilter contains fields upon which a service can be filtered.
type ServiceFilter struct {
	ID   *int
	Name *string
}
