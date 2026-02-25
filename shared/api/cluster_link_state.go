package api

const (
	// ClusterLinkMemberStatusActive represents a cluster link member that is reachable and returns trusted auth status.
	ClusterLinkMemberStatusActive = "Active"

	// ClusterLinkMemberStatusUnreachable represents a cluster link member that is unreachable.
	ClusterLinkMemberStatusUnreachable = "Unreachable"

	// ClusterLinkMemberStatusUnauthenticated represents a cluster link member that is reachable and returns untrusted auth status.
	ClusterLinkMemberStatusUnauthenticated = "Unauthenticated"
)

// ClusterLinkMemberState represents the state of a cluster member on a linked cluster.
//
// swagger:model
//
// API extension: cluster_links.
type ClusterLinkMemberState struct {
	// Name of the cluster member.
	// Example: lxd01
	ServerName string `json:"server_name" yaml:"server_name"`

	// Address at which the cluster member can be reached.
	// Example: 10.0.0.1:8443
	Address string `json:"address" yaml:"address"`

	// Cluster member's status.
	// Example: active
	Status string `json:"status" yaml:"status"`
}

// ClusterLinkState represents the state of a linked cluster.
//
// swagger:model
//
// API extension: cluster_links.
type ClusterLinkState struct {
	// ClusterLinkMembers represents the state of cluster members on a linked cluster.
	// Example: ["lxd01", "lxd02"]
	ClusterLinkMembersState []ClusterLinkMemberState `json:"cluster_link_members" yaml:"cluster_link_members"`
}
