package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"

	"github.com/canonical/lxd/lxd/events"
)

// A Daemon can respond to requests from a shared client.
type Daemon struct {
	// Logging
	debug   bool
	verbose bool

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
	return &Daemon{
		debug:       debug,
		verbose:     verbose,
		chConnected: make(chan struct{}),
	}
}

// init initialises the Daemon.
func (d *Daemon) init() error {
	var err error

	// Set the event server.
	d.events, err = events.NewServer(d.debug, d.verbose, nil)
	if err != nil {
		return fmt.Errorf("Failed to set up event server: %w", err)
	}

	// Start the server.
	err = startHTTPServer(d)
	if err != nil {
		return fmt.Errorf("Failed to start HTTP server: %w", err)
	}

	// Check whether we should start the devlxd server in the early setup. This way, /dev/lxd/sock
	// will be available for any systemd services starting after the lxd-agent.
	f, err := os.Open("agent.conf")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return err
	}

	err = setConnectionInfo(d, f)
	if err != nil {
		_ = f.Close()
		return err
	}

	_ = f.Close()

	if d.devlxdEnabled {
		err = startDevlxdServer(d)
		if err != nil {
			return err
		}
	}

	return nil
}
