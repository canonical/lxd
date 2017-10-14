package api

// ClusterPost represents the fields required to bootstrap or join a LXD
// cluster.
//
// API extension: cluster
type ClusterPost struct {
	Name string `json:"name" yaml:"name"`
}
