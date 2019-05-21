package client

import (
	"context"

	"github.com/CanonicalLtd/go-dqlite/internal/bindings"
)

// ServerInfo holds information about a single server.
type ServerInfo = bindings.ServerInfo

// ServerStore is used by a dqlite client to get an initial list of candidate
// dqlite servers that it can dial in order to find a leader server to connect
// to.
//
// Once connected, the client periodically updates the server addresses in the
// store by querying the leader about changes in the cluster (such as servers
// being added or removed).
type ServerStore interface {
	// Get return the list of known servers.
	Get(context.Context) ([]ServerInfo, error)

	// Set updates the list of known cluster servers.
	Set(context.Context, []ServerInfo) error
}

// InmemServerStore keeps the list of servers in memory.
type InmemServerStore struct {
	servers []ServerInfo
}

// NewInmemServerStore creates ServerStore which stores its data in-memory.
func NewInmemServerStore() *InmemServerStore {
	return &InmemServerStore{
		servers: make([]ServerInfo, 0),
	}
}

// Get the current servers.
func (i *InmemServerStore) Get(ctx context.Context) ([]ServerInfo, error) {
	return i.servers, nil
}

// Set the servers.
func (i *InmemServerStore) Set(ctx context.Context, servers []ServerInfo) error {
	i.servers = servers
	return nil
}
