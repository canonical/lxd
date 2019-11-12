package main

import (
	"github.com/lxc/lxd/lxd/events"
)

// A Daemon can respond to requests from a shared client.
type Daemon struct {
	// Event servers
	events *events.Server
}

// NewDaemon returns a new Daemon object with the given configuration.
func NewDaemon(debug, verbose bool) *Daemon {
	lxdEvents := events.NewServer(debug, verbose)

	return &Daemon{
		events: lxdEvents,
	}
}
