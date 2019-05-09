package cluster

import (
	"github.com/hashicorp/raft"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
)

// Raft returns the gateway's internal raft instance.
func (g *Gateway) Raft() *raft.Raft {
	return g.raft.raft
}

// IsLeader returns true if this node is the leader.
func (g *Gateway) IsLeader() bool {
	return g.isLeader()
}

// Cert returns the gateway's internal TLS certificate information.
func (g *Gateway) Cert() *shared.CertInfo {
	return g.cert
}

// RaftNodes returns the nodes currently part of the raft cluster.
func (g *Gateway) RaftNodes() ([]db.RaftNode, error) {
	return g.currentRaftNodes()
}
