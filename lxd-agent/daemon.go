package main

import (
	"github.com/lxc/lxd/lxd/events"
)

// A Daemon can respond to requests from a shared client.
type Daemon struct {
	// Event servers
	events *events.Server
}

// newDaemon returns a new Daemon object with the given configuration.
func newDaemon(debug, verbose bool) *Daemon {
	lxdEvents := events.NewServer(debug, verbose)

	return &Daemon{
		events: lxdEvents,
	}
}
