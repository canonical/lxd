package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/grant-he/lxd/lxd/instance/instancetype"
	"github.com/grant-he/lxd/lxd/util"
	"github.com/grant-he/lxd/lxd/vsock"
	"github.com/grant-he/lxd/shared"
	"github.com/grant-he/lxd/shared/logger"
	"github.com/grant-he/lxd/shared/logging"
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

	logger.Info("Starting")
	defer logger.Info("Stopped")

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
		logger.Info("Seeding cloud-init")

		cloudInitPath := "/run/cloud-init"
		if shared.PathExists(cloudInitPath) {
			logger.Info(fmt.Sprintf("Removing %q", cloudInitPath))
			err = os.RemoveAll(cloudInitPath)
			if err != nil {
				return err
			}
		}

		shared.RunCommand("systemctl", "reboot")

		// Wait up to 5min for the reboot to actually happen, if it doesn't, then move on to allowing connections.
		time.Sleep(300 * time.Second)
	}

	// Mount shares from host.
	c.mountHostShares()

	// Done with early setup, tell systemd to continue boot.
	// Allows a service that needs a file that's generated by the agent to be able to declare After=lxd-agent
	// and know the file will have been created by the time the service is started.
	if os.Getenv("NOTIFY_SOCKET") != "" {
		shared.RunCommand("systemd-notify", "READY=1")
	}

	// Load the kernel driver.
	err = util.LoadModule("vsock")
	if err != nil {
		return errors.Wrap(err, "Unable to load the vsock kernel module")
	}

	// Setup the listener.
	l, err := vsock.Listen(shared.DefaultPort)
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

	d := newDaemon(c.global.flagLogDebug, c.global.flagLogVerbose)

	servers := make(map[string]*http.Server, 2)

	// Prepare the HTTP server.
	servers["http"] = restServer(tlsConfig, cert, c.global.flagLogDebug, d)

	// Prepare the devlxd server.
	devlxdListener, err := createDevLxdlListener("/dev")
	if err != nil {
		return err
	}

	servers["devlxd"] = devLxdServer(d)

	// Create a cancellation context.
	ctx, cancelFunc := context.WithCancel(context.Background())

	// Start status notifier in background.
	go c.startStatusNotifier(ctx)

	errChan := make(chan error, 1)

	// Start the server.
	go func() {
		err := servers["http"].ServeTLS(networkTLSListener(l, tlsConfig), "agent.crt", "agent.key")
		if err != nil {
			errChan <- err
		}
	}()

	// Only start the devlxd listener if instance-data is present.
	if shared.PathExists("instance-data") {
		go func() {
			err := servers["devlxd"].Serve(devlxdListener)
			if err != nil {
				errChan <- err
			}
		}()
	}

	// Cancel context when SIGTEM is received.
	chSignal := make(chan os.Signal, 1)
	signal.Notify(chSignal, unix.SIGTERM)

	select {
	case <-chSignal:
		cancelFunc()
		os.Exit(0)
	case err := <-errChan:
		fmt.Fprintln(os.Stderr, err)
		cancelFunc()
		os.Exit(1)
	}

	return nil
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

// mountHostShares reads the agent-mounts.json file from config share and mounts the shares requested.
func (c *cmdAgent) mountHostShares() {
	agentMountsFile := "./agent-mounts.json"
	if !shared.PathExists(agentMountsFile) {
		return
	}

	b, err := ioutil.ReadFile(agentMountsFile)
	if err != nil {
		logger.Errorf("Failed to load agent mounts file %q: %v", agentMountsFile, err)
	}

	var agentMounts []instancetype.VMAgentMount
	err = json.Unmarshal(b, &agentMounts)
	if err != nil {
		logger.Errorf("Failed to parse agent mounts file %q: %v", agentMountsFile, err)
		return
	}

	for _, mount := range agentMounts {
		// Convert relative mounts to absolute from / otherwise dir creation fails or mount fails.
		if !strings.HasPrefix(mount.Target, "/") {
			mount.Target = fmt.Sprintf("/%s", mount.Target)
		}

		if !shared.PathExists(mount.Target) {
			err := os.MkdirAll(mount.Target, 0755)
			if err != nil {
				logger.Errorf("Failed to create mount target %q", mount.Target)
				continue // Don't try to mount if mount point can't be created.
			}
		}

		if mount.FSType == "9p" {
			// Before mounting with 9p, try virtio-fs and use 9p as the fallback.
			args := []string{"-t", "virtiofs", mount.Source, mount.Target}

			for _, opt := range mount.Options {
				// Ignore the 'trans-virtio' mount option as that's specific to 9p.
				if opt != "trans=virtio" {
					args = append(args, "-o", opt)
				}
			}

			_, err = shared.RunCommand("mount", args...)
			if err == nil {
				logger.Infof("Mounted %q (Type: %q, Options: %v) to %q", mount.Source, "virtiofs", mount.Options, mount.Target)
				continue
			}
		}

		args := []string{"-t", mount.FSType, mount.Source, mount.Target}

		for _, opt := range mount.Options {
			args = append(args, "-o", opt)
		}

		_, err = shared.RunCommand("mount", args...)
		if err != nil {
			logger.Errorf("Failed mount %q (Type: %q, Options: %v) to %q: %v", mount.Source, mount.FSType, mount.Options, mount.Target, err)
			continue
		}

		logger.Infof("Mounted %q (Type: %q, Options: %v) to %q", mount.Source, mount.FSType, mount.Options, mount.Target)
	}
}
