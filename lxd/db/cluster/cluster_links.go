package cluster

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t cluster_links.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e cluster_link objects table=cluster_links
//go:generate mapper stmt -e cluster_link objects-by-ID table=cluster_links
//go:generate mapper stmt -e cluster_link objects-by-Name table=cluster_links
//go:generate mapper stmt -e cluster_link id table=cluster_links
//go:generate mapper stmt -e cluster_link create table=cluster_links
//go:generate mapper stmt -e cluster_link delete-by-Name table=cluster_links
//go:generate mapper stmt -e cluster_link update table=cluster_links
//go:generate mapper stmt -e cluster_link rename table=cluster_links
//
//go:generate mapper method -i -e cluster_link GetMany
//go:generate mapper method -i -e cluster_link GetOne
//go:generate mapper method -i -e cluster_link ID
//go:generate mapper method -i -e cluster_link Exists
//go:generate mapper method -i -e cluster_link Create
//go:generate mapper method -i -e cluster_link DeleteOne-by-Name
//go:generate mapper method -i -e cluster_link Update
//go:generate mapper method -i -e cluster_link Rename
//go:generate goimports -w cluster_links.mapper.go
//go:generate goimports -w cluster_links.interface.mapper.go

// ClusterLink is the database representation of an api.ClusterLink.
type ClusterLink struct {
	ID          int
	IdentityID  int
	Name        string `db:"primary=true"`
	Addresses   string
	Description string `db:"coalesce=''"`
}

// ClusterLinkFilter contains fields upon which a cluster link can be filtered.
type ClusterLinkFilter struct {
	ID   *int
	Name *string
}
