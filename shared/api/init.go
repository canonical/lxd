package api

// InitPreseed represents initialization configuration that can be supplied to `lxd init`.
//
// swagger:model
//
// API extension: preseed.
type InitPreseed struct {
	Node    InitLocalPreseed    `yaml:",inline"`
	Cluster *InitClusterPreseed `json:"cluster" yaml:"cluster"`
}

// InitLocalPreseed represents initialization configuration for the local LXD.
//
// swagger:model
//
// API extension: preseed.
type InitLocalPreseed struct {
	ServerPut `yaml:",inline"`

	// Networks by project to add to LXD
	// Example: Network on the "default" project
	Networks []InitNetworksProjectPost `json:"networks" yaml:"networks"`

	// Storage Pools to add to LXD
	// Example: local dir storage pool
	StoragePools []StoragePoolsPost `json:"storage_pools" yaml:"storage_pools"`

	// Profiles to add to LXD
	// Example: "default" profile with a root disk device
	Profiles []ProfilesPost `json:"profiles" yaml:"profiles"`

	// Projects to add to LXD
	// Example: "default" project
	Projects []ProjectsPost `json:"projects" yaml:"projects"`
}

// InitNetworksProjectPost represents the fields of a new LXD network along with its associated project.
//
// swagger:model
//
// API extension: preseed.
type InitNetworksProjectPost struct {
	NetworksPost `yaml:",inline"`

	// Project in which the network will reside
	// Example: "default"
	Project string
}

// InitClusterPreseed represents initialization configuration for the LXD cluster.
//
// swagger:model
//
// API extension: preseed.
type InitClusterPreseed struct {
	ClusterPut `yaml:",inline"`

	// The path to the cluster certificate
	// Example: /tmp/cluster.crt
	ClusterCertificatePath string `json:"cluster_certificate_path" yaml:"cluster_certificate_path"`

	// A cluster join token
	// Example: BASE64-TOKEN
	ClusterToken string `json:"cluster_token" yaml:"cluster_token"`
}
