package cluster

import (
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
)

// Export raft-related APIs for black box unit testing.
func NewRaft(db *db.Node, cert *shared.CertInfo, latency float64) (*RaftInstance, error) {
	instance, err := newRaft(db, cert, latency)
	if err != nil {
		return nil, err
	}
	return &RaftInstance{*instance}, nil
}

type RaftInstance struct {
	raftInstance
}
