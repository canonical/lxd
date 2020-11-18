// +build linux,cgo,!agent

package state

import (
	"context"
	"net/http"
	"net/url"

	"github.com/grant-he/lxd/lxd/db"
	"github.com/grant-he/lxd/lxd/endpoints"
	"github.com/grant-he/lxd/lxd/events"
	"github.com/grant-he/lxd/lxd/firewall"
	"github.com/grant-he/lxd/lxd/maas"
	"github.com/grant-he/lxd/lxd/sys"
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
	OS    *sys.OS
	Proxy func(req *http.Request) (*url.URL, error)

	// LXD server
	Endpoints *endpoints.Endpoints

	// Event server
	DevlxdEvents *events.Server
	Events       *events.Server

	// Firewall instance
	Firewall firewall.Firewall

	Context context.Context
}

// NewState returns a new State object with the given database and operating
// system components.
func NewState(ctx context.Context, node *db.Node, cluster *db.Cluster, maas *maas.Controller, os *sys.OS, endpoints *endpoints.Endpoints, events *events.Server, devlxdEvents *events.Server, firewall firewall.Firewall, proxy func(req *http.Request) (*url.URL, error)) *State {
	return &State{
		Node:         node,
		Cluster:      cluster,
		MAAS:         maas,
		OS:           os,
		Endpoints:    endpoints,
		DevlxdEvents: devlxdEvents,
		Events:       events,
		Firewall:     firewall,
		Proxy:        proxy,
		Context:      ctx,
	}
}
