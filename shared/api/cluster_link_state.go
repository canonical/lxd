package api

// ClusterLinkState represents the state of a cluster link.
//
// swagger:model
//
// API extension: cluster_links.
type ClusterLinkState struct {
	ClusterLinkMembers []ClusterMember `json:"cluster_link_members" yaml:"cluster_link_members"`
}
