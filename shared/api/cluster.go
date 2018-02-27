package api

// Cluster represents high-level information about a LXD cluster.
//
// API extension: clustering
type Cluster struct {
	ServerName string `json:"server_name" yaml:"server_name"`
	Enabled    bool   `json:"enabled" yaml:"enabled"`
}

// ClusterPut represents the fields required to bootstrap or join a LXD
// cluster.
//
// API extension: clustering
type ClusterPut struct {
	Cluster            `yaml:",inline"`
	ClusterAddress     string `json:"cluster_address" yaml:"cluster_address"`
	ClusterCertificate string `json:"cluster_certificate" yaml:"cluster_certificate"`
}

// ClusterMemberPost represents the fields required to rename a LXD node.
//
// API extension: clustering
type ClusterMemberPost struct {
	ServerName string `json:"server_name" yaml:"server_name"`
}

// ClusterMember represents the a LXD node in the cluster.
//
// API extension: clustering
type ClusterMember struct {
	ServerName string `json:"server_name" yaml:"server_name"`
	URL        string `json:"url" yaml:"url"`
	Database   bool   `json:"database" yaml:"database"`
	Status     string `json:"status" yaml:"status"`
	Message    string `json:"message" yaml:"message"`
}
