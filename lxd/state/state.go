package state

import (
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/endpoints"
	"github.com/lxc/lxd/lxd/events"
	"github.com/lxc/lxd/lxd/maas"
	"github.com/lxc/lxd/lxd/sys"
)

// State is a gateway to the two main stateful components of LXD, the database
// and the operating system. It's typically used by model entities such as
// containers, volumes, etc. in order to perform changes.
type State struct {
	// Databases
	Node    *db.Node
	Cluster *db.Cluster

	// MAAS server
	MAAS *maas.Controller

	// OS access
	OS *sys.OS

	// LXD server
	Endpoints *endpoints.Endpoints

	// Event server
	DevlxdEvents *events.Server
	Events       *events.Server
}

// NewState returns a new State object with the given database and operating
// system components.
func NewState(node *db.Node, cluster *db.Cluster, maas *maas.Controller, os *sys.OS, endpoints *endpoints.Endpoints, events *events.Server, devlxdEvents *events.Server) *State {
	return &State{
		Node:         node,
		Cluster:      cluster,
		MAAS:         maas,
		OS:           os,
		Endpoints:    endpoints,
		DevlxdEvents: devlxdEvents,
		Events:       events,
	}
}
