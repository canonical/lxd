package cluster

import (
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
)

// IsLeader returns true if this node is the leader.
func (g *Gateway) IsLeader() (bool, error) {
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
