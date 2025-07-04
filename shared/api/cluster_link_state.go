package api

const (
	// ClusterLinkMemberStatusActive represents a cluster link member that is reachable and returns trusted auth status.
	ClusterLinkMemberStatusActive = "ACTIVE"

	// ClusterLinkMemberStatusUnreachable represents a cluster link member that is unreachable.
	ClusterLinkMemberStatusUnreachable = "UNREACHABLE"

	// ClusterLinkMemberStatusUnauthenticated represents a cluster link member that is reachable and returns untrusted auth status.
	ClusterLinkMemberStatusUnauthenticated = "UNAUTHENTICATED"
)

// ClusterLinkMember represents a LXD node in the cluster link.
//
// swagger:model
//
// API extension: cluster_links.
type ClusterLinkMember struct {
	// Name of the cluster link member answering the request
	// Example: lxd01
	ServerName string `json:"server_name" yaml:"server_name"`

	// Address at which the cluster link member can be reached.
	// Example: 10.0.0.1:8443
	Address string `json:"address" yaml:"address"`

	// The cluster link member's status.
	// Example: ACTIVE
	Status string `json:"status" yaml:"status"`
}

// ClusterLinkState represents the state of a cluster link.
//
// swagger:model
//
// API extension: cluster_links.
type ClusterLinkState struct {
	ClusterLinkMembers []ClusterLinkMember `json:"cluster_link_members" yaml:"cluster_link_members"`
}
