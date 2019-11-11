package main

import (
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxd/vsock"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
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
	// Setup logger.
	log, err := logging.GetLogger("lxd-agent", "", c.global.flagLogVerbose, c.global.flagLogDebug, nil)
	if err != nil {
		os.Exit(1)
	}
	logger.Log = log

	logger.Info("lxd-agent starting")
	defer logger.Info("lxd-agent stopped")

	// Setup cloud-init.
	if shared.PathExists("/etc/cloud") && !shared.PathExists("/var/lib/cloud/seed/nocloud-net") {
		err := os.MkdirAll("/var/lib/cloud/seed/nocloud-net/", 0700)
		if err != nil {
			return err
		}

		for _, fName := range []string{"meta-data", "user-data", "vendor-data", "network-config"} {
			if !shared.PathExists(filepath.Join("cloud-init", fName)) {
				continue
			}

			err := shared.FileCopy(filepath.Join("cloud-init", fName), filepath.Join("/var/lib/cloud/seed/nocloud-net", fName))
			if err != nil {
				return err
			}
		}

		if shared.PathExists("/run/cloud-init") {
			err = os.RemoveAll("/run/cloud-init")
			if err != nil {
				return err
			}
		}

		shared.RunCommand("systemctl", "daemon-reload")
		shared.RunCommand("systemctl", "start", "cloud-init.target")
	}

	// Setup the listener.
	l, err := vsock.Listen(8443)
	if err != nil {
		return errors.Wrap(err, "Failed to listen on vsock")
	}

	// Load the expected server certificate.
	cert, err := shared.ReadCert("server.crt")
	if err != nil {
		return errors.Wrap(err, "Failed to read client certificate")
	}

	tlsConfig, err := serverTLSConfig()
	if err != nil {
		return errors.Wrap(err, "Failed to get TLS config")
	}

	// Prepare the HTTP server.
	httpServer := restServer(tlsConfig, cert, c.global.flagLogDebug)

	// Start the server.
	return httpServer.ServeTLS(networkTLSListener(l, tlsConfig), "agent.crt", "agent.key")
}
