package dqlite

import (
	"fmt"

	"github.com/CanonicalLtd/go-dqlite/internal/bindings"
	"github.com/CanonicalLtd/go-dqlite/internal/logging"
	"github.com/CanonicalLtd/go-dqlite/internal/registry"
)

// Registry tracks internal data shared by the dqlite Driver and FSM.
type Registry struct {
	name     string
	vfs      *bindings.Vfs
	logger   *bindings.Logger
	registry *registry.Registry
}

// NewRegistry creates a new Registry, which is expected to be passed to both
// NewFSM and NewDriver.
//
// The ID parameter is a string identifying the local node.
func NewRegistry(id string) *Registry {
	return NewRegistryWithLogger(id, logging.Stdout())
}

// NewRegistryWithLogger returns a registry configured with the given logger.
func NewRegistryWithLogger(id string, log LogFunc) *Registry {
	name := fmt.Sprintf("dqlite-%s", id)

	logger := bindings.NewLogger(log)

	vfs, err := bindings.NewVfs(name, logger)
	if err != nil {
		panic("failed to register VFS")
	}

	return &Registry{
		name:     name,
		vfs:      vfs,
		registry: registry.New(vfs),
		logger:   logger,
	}
}

// Close the registry.
func (r *Registry) Close() {
	r.vfs.Close()
	r.logger.Close()
}
