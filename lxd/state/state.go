package state

import (
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/endpoints"
	"github.com/lxc/lxd/lxd/maas"
	"github.com/lxc/lxd/lxd/sys"
)

// State is a gateway to the two main stateful components of LXD, the database
// and the operating system. It's typically used by model entities such as
// containers, volumes, etc. in order to perform changes.
type State struct {
	Node      *db.Node
	Cluster   *db.Cluster
	MAAS      *maas.Controller
	OS        *sys.OS
	Endpoints *endpoints.Endpoints
}

// NewState returns a new State object with the given database and operating
// system components.
func NewState(node *db.Node, cluster *db.Cluster, maas *maas.Controller, os *sys.OS, endpoints *endpoints.Endpoints) *State {
	return &State{
		Node:      node,
		Cluster:   cluster,
		MAAS:      maas,
		OS:        os,
		Endpoints: endpoints,
	}
}
