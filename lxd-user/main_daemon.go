package main

import (
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/util"
)

var (
	connections     int64
	connectionsLock sync.Mutex
)

type cmdDaemon struct {
	global *cmdGlobal
}

func (c *cmdDaemon) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "lxd-user"
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDaemon) Run(cmd *cobra.Command, args []string) error {
	// Setup logger.
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	log.SetLevel(log.DebugLevel)
	log.SetOutput(os.Stdout)

	// Connect to LXD.
	log.Info("Connecting to LXD")
	client, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return fmt.Errorf("Unable to connect to LXD: %w", err)
	}

	// Validate LXD configuration.
	ok, err := lxdIsConfigured(client)
	if err != nil {
		return fmt.Errorf("Failed to check LXD configuration: %w", err)
	}

	if !ok {
		log.Info("Performing initial LXD configuration")
		err = lxdInitialConfiguration(client)
		if err != nil {
			return fmt.Errorf("Failed to apply initial LXD configuration: %w", err)
		}
	}

	// Disconnect from LXD.
	client.Disconnect()

	// Setup the unix socket.
	listeners := util.GetListeners(util.SystemdListenFDsStart)
	if len(listeners) > 1 {
		return fmt.Errorf("More than one socket-activation FD received")
	}

	var listener *net.UnixListener
	if len(listeners) == 1 {
		// Handle socket activation.
		unixListener, ok := listeners[0].(*net.UnixListener)
		if !ok {
			return fmt.Errorf("Socket-activation FD isn't a unix socket")
		}

		listener = unixListener

		// Automatically shutdown after inactivity.
		go func() {
			for {
				time.Sleep(30 * time.Second)

				connectionsLock.Lock()
				if connections == 0 {
					// Exit if no more connections.
					log.Info("Shutting down for inactivity")
					os.Exit(0)
				}
				connectionsLock.Unlock()
			}
		}()
	} else {
		// Create our own socket.
		unixPath := "unix.socket"
		os.Remove(unixPath)

		unixAddr, err := net.ResolveUnixAddr("unix", unixPath)
		if err != nil {
			return fmt.Errorf("Unable to resolve unix socket: %w", err)
		}

		server, err := net.ListenUnix("unix", unixAddr)
		if err != nil {
			return fmt.Errorf("Unable to setup unix socket: %w", err)
		}
		server.SetUnlinkOnClose(true)

		listener = server
	}

	// Start accepting requests.
	log.Info("Starting up the server")

	for {
		// Accept new connection.
		conn, err := listener.AcceptUnix()
		if err != nil {
			log.Error("Failed to accept new connection: %w", err)
			continue
		}

		go proxyConnection(conn)
	}
}
