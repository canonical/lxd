package main

import (
	"sync"

	"github.com/canonical/lxd/lxd/events"
)

// A Daemon can respond to requests from a shared client.
type Daemon struct {
	// Event servers
	events *events.Server

	// ContextID and port of the LXD VM socket server.
	serverCID         uint32
	serverPort        uint32
	serverCertificate string

	// The channel which is used to indicate that the lxd-agent was able to connect to LXD.
	chConnected chan struct{}

	devlxdRunning bool
	devlxdMu      sync.Mutex
	devlxdEnabled bool
}

// newDaemon returns a new Daemon object with the given configuration.
func newDaemon(debug, verbose bool) *Daemon {
	lxdEvents := events.NewServer(debug, verbose, nil)

	return &Daemon{
		events:      lxdEvents,
		chConnected: make(chan struct{}),
	}
}
