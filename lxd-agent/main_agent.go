package main

import (
	"log"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxd/vsock"
	"github.com/lxc/lxd/shared"
)

type cmdAgent struct {
	global *cmdGlobal
}

func (c *cmdAgent) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "lxd-agent [--debug]"
	cmd.Short = "LXD virtual machine agent"
	cmd.Long = `Description:
  LXD virtual machine agent

  This daemon is to be run inside virtual machines managed by LXD.
  It will normally be started through init scripts present or injected
  into the virtual machine.
`
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdAgent) Run(cmd *cobra.Command, args []string) error {
	// Setup the listener.
	l, err := vsock.Listen(8443)
	if err != nil {
		log.Fatalln(errors.Wrap(err, "Failed to listen on vsock"))
	}

	// Load the expected server certificate.
	cert, err := shared.ReadCert("server.crt")
	if err != nil {
		log.Fatalln(errors.Wrap(err, "Failed to read client certificate"))
	}

	tlsConfig, err := serverTLSConfig()
	if err != nil {
		log.Fatalln(errors.Wrap(err, "Failed to get TLS config"))
	}

	// Prepare the HTTP server.
	httpServer := restServer(tlsConfig, cert, c.global.flagLogDebug)

	// Start the server
	return httpServer.ServeTLS(networkTLSListener(l, tlsConfig), "agent.crt", "agent.key")
}
