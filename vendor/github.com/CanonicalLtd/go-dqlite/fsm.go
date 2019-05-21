package dqlite

import (
	"github.com/CanonicalLtd/go-dqlite/internal/replication"
	"github.com/hashicorp/raft"
)

// NewFSM creates a new dqlite FSM, suitable to be passed to raft.NewRaft.
//
// It will handle replication of the SQLite write-ahead log.
//
// This is mostly an internal implementation detail of dqlite, but it needs to
// be exposed since the raft.Raft parameter that NewDriver accepts doesn't
// allow access to the FSM that it was passed when created with raft.NewRaft().
func NewFSM(r *Registry) raft.FSM {
	return replication.NewFSM(r.registry)
}
