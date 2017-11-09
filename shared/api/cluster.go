package api

// Cluster represents high-level information about a LXD cluster.
type Cluster struct {
	StoragePools []StoragePool `json:"storage_pools" yaml:"storage_pools"`
	Networks     []Network     `json:"networks" yaml:"networks"`
}

// ClusterPost represents the fields required to bootstrap or join a LXD
// cluster.
//
// API extension: cluster
type ClusterPost struct {
	Name           string `json:"name" yaml:"name"`
	Address        string `json:"address" yaml:"address"`
	Schema         int    `json:"schema" yaml:"schema"`
	API            int    `json:"api" yaml:"api"`
	TargetAddress  string `json:"target_address" yaml:"target_address"`
	TargetCert     string `json:"target_cert" yaml:"target_cert"`
	TargetCA       []byte `json:"target_ca" yaml:"target_ca"`
	TargetPassword string `json:"target_password" yaml:"target_password"`
}

// ClusterNodeAccepted represents the response of a request to join a cluster.
//
// API extension: cluster
type ClusterNodeAccepted struct {
	RaftNodes  []RaftNode `json:"raft_nodes" yaml:"raft_nodes"`
	PrivateKey []byte     `json:"private_key" yaml:"private_key"`
}

// RaftNode represents the a LXD node that is part of the dqlite raft cluster.
//
// API extension: cluster
type RaftNode struct {
	ID      int64  `json:"id" yaml:"id"`
	Address string `json:"address" yaml:"address"`
}

// Node represents the a LXD node in the cluster.
type Node struct {
	Name     string `json:"name" yaml:"name"`
	URL      string `json:"url" yaml:"url"`
	Database bool   `json:"database" yaml:"database"`
	State    string `json:"state" yaml:"state"`
}
