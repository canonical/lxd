package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

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

	// Apply the templated files.
	files, err := templatesApply("files/")
	if err != nil {
		return err
	}

	// Sync the hostname.
	if shared.PathExists("/proc/sys/kernel/hostname") && shared.StringInSlice("/etc/hostname", files) {
		// Open the two files.
		src, err := os.Open("/etc/hostname")
		if err != nil {
			return err
		}

		dst, err := os.Create("/proc/sys/kernel/hostname")
		if err != nil {
			return err
		}

		// Copy the data.
		_, err = io.Copy(dst, src)
		if err != nil {
			return err
		}

		// Close the files.
		src.Close()
		dst.Close()
	}

	// Run cloud-init.
	if shared.PathExists("/etc/cloud") && shared.StringInSlice("/var/lib/cloud/seed/nocloud-net/meta-data", files) {
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

	d := NewDaemon(c.global.flagLogDebug, c.global.flagLogVerbose)

	// Prepare the HTTP server.
	httpServer := restServer(tlsConfig, cert, c.global.flagLogDebug, d)

	// Create a cancellation context.
	ctx, cancelFunc := context.WithCancel(context.Background())

	// Start status notifier in background.
	go c.startStatusNotifier(ctx)

	// Cancel context when SIGTEM is received.
	chSignal := make(chan os.Signal, 1)
	signal.Notify(chSignal, unix.SIGTERM)
	go func() {
		<-chSignal
		cancelFunc()
		os.Exit(0)
	}()

	// Start the server.
	return httpServer.ServeTLS(networkTLSListener(l, tlsConfig), "agent.crt", "agent.key")
}

// startStatusNotifier sends status of agent to vserial ring buffer every 10s or when context is done.
func (c *cmdAgent) startStatusNotifier(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(time.Second) * 10)
	defer ticker.Stop()

	// Write initial started status.
	c.writeStatus("STARTED")

	for {
		select {
		case <-ticker.C:
			// Re-populate status periodically in case LXD restarts.
			c.writeStatus("STARTED")
		case <-ctx.Done():
			// Indicate we are stopping to LXD.
			c.writeStatus("STOPPED")
			return
		}
	}
}

// writeStatus writes a status code to the vserial ring buffer used to detect agent status on host.
func (c *cmdAgent) writeStatus(status string) error {
	if shared.PathExists("/dev/virtio-ports/org.linuxcontainers.lxd") {
		vSerial, err := os.OpenFile("/dev/virtio-ports/org.linuxcontainers.lxd", os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		vSerial.Write([]byte(fmt.Sprintf("%s\n", status)))
		vSerial.Close()
	}

	return nil
}
