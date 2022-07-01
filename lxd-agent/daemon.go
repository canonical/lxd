package main

import (
	"github.com/lxc/lxd/lxd/events"
)

// A Daemon can respond to requests from a shared client.
type Daemon struct {
	// Event servers
	events *events.Server

	// The channel which is used to indicate that the lxd-agent was able to connect to LXD.
	chConnected chan struct{}
}

// newDaemon returns a new Daemon object with the given configuration.
func newDaemon(debug, verbose bool) *Daemon {
	lxdEvents := events.NewServer(debug, verbose, nil)

	return &Daemon{
		events:      lxdEvents,
		chConnected: make(chan struct{}),
	}
}
