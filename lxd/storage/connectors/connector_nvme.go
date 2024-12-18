package connectors

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
)

var _ Connector = &connectorNVMe{}

type connectorNVMe struct {
	common
}

// Type returns the type of the connector.
func (c *connectorNVMe) Type() string {
	return TypeNVME
}

// Version returns the version of the NVMe CLI.
func (c *connectorNVMe) Version() (string, error) {
	// Detect and record the version of the NVMe CLI.
	out, err := shared.RunCommand("nvme", "version")
	if err != nil {
		return "", fmt.Errorf("Failed to get nvme-cli version: %w", err)
	}

	fields := strings.Split(strings.TrimSpace(out), " ")
	if strings.HasPrefix(out, "nvme version ") && len(fields) > 2 {
		return fmt.Sprintf("%s (nvme-cli)", fields[2]), nil
	}

	return "", fmt.Errorf("Failed to get nvme-cli version: Unexpected output %q", out)
}

// LoadModules loads the NVMe/TCP kernel modules.
// Returns true if the modules can be loaded.
func (c *connectorNVMe) LoadModules() bool {
	err := util.LoadModule("nvme_fabrics")
	if err != nil {
		return false
	}

	err = util.LoadModule("nvme_tcp")
	return err == nil
}

// QualifiedName returns a custom NQN generated from the server UUID.
// Getting the NQN from /etc/nvme/hostnqn would require the nvme-cli
// package to be installed on the host.
func (c *connectorNVMe) QualifiedName() (string, error) {
	return fmt.Sprintf("nqn.2014-08.org.nvmexpress:uuid:%s", c.serverUUID), nil
}

// SessionID returns the target's qualified name (NQN) if a corresponding
// session is found. Otherwise, an empty string is returned.
func (c *connectorNVMe) SessionID(targetQN string) (string, error) {
	// Base path for NVMe sessions/subsystems.
	basePath := "/sys/devices/virtual/nvme-subsystem"

	// Retrieve list of existing NVMe sessions on this host.
	directories, err := os.ReadDir(basePath)
	if err != nil {
		if os.IsNotExist(err) {
			// No active sessions because NVMe subsystems directory does not exist.
			return "", nil
		}

		return "", fmt.Errorf("Failed getting a list of existing NVMe subsystems: %w", err)
	}

	for _, directory := range directories {
		subsystemName := directory.Name()

		// Get the target NQN.
		nqnBytes, err := os.ReadFile(filepath.Join(basePath, subsystemName, "subsysnqn"))
		if err != nil {
			return "", fmt.Errorf("Failed getting the target NQN for subystem %q: %w", subsystemName, err)
		}

		if strings.Contains(string(nqnBytes), targetQN) {
			// Already connected.
			return targetQN, nil
		}
	}

	return "", nil
}

// Connect establishes a connection with the target on the given address.
func (c *connectorNVMe) Connect(ctx context.Context, targetAddr string, targetQN string) error {
	hostNQN, err := c.QualifiedName()
	if err != nil {
		return err
	}

	// Try to find an existing NVMe session.
	targetNQN, err := c.SessionID(targetQN)
	if err != nil {
		return err
	}

	if targetNQN != "" {
		// Already connected.
		return nil
	}

	_, stderr, err := shared.RunCommandSplit(ctx, nil, nil, "nvme", "connect", "--transport", "tcp", "--traddr", targetAddr, "--nqn", targetQN, "--hostnqn", hostNQN, "--hostid", c.serverUUID)
	if err != nil {
		return fmt.Errorf("Failed to connect to target %q on %q via NVMe: %w", targetQN, targetAddr, err)
	}

	if stderr != "" {
		return fmt.Errorf("Failed to connect to target %q on %q via NVMe: %s", targetQN, targetAddr, stderr)
	}

	return nil
}

// ConnectAll establishes a connection with all targets available on the given address.
func (c *connectorNVMe) ConnectAll(ctx context.Context, targetAddr string) error {
	hostNQN, err := c.QualifiedName()
	if err != nil {
		return err
	}

	_, stderr, err := shared.RunCommandSplit(ctx, nil, nil, "nvme", "connect-all", "--transport", "tcp", "--traddr", targetAddr, "--hostnqn", hostNQN, "--hostid", c.serverUUID)
	if err != nil {
		return fmt.Errorf("Failed to connect to any target on %q via NVMe: %w", targetAddr, err)
	}

	if stderr != "" {
		return fmt.Errorf("Failed to connect to any target on %q via NVMe: %s", targetAddr, stderr)
	}

	return nil
}

// Disconnect terminates a connection with the target.
func (c *connectorNVMe) Disconnect(targetQN string) error {
	// Find an existing NVMe session.
	targetNQN, err := c.SessionID(targetQN)
	if err != nil {
		return err
	}

	// Disconnect from the NVMe target if there is an existing session.
	if targetNQN != "" {
		_, err := shared.RunCommand("nvme", "disconnect", "--nqn", targetNQN)
		if err != nil {
			return fmt.Errorf("Failed disconnecting from NVMe target %q: %w", targetNQN, err)
		}
	}

	return nil
}

// DisconnectAll terminates all connections with all targets.
func (c *connectorNVMe) DisconnectAll() error {
	_, err := shared.RunCommand("nvme", "disconnect-all")
	if err != nil {
		return fmt.Errorf("Failed disconnecting from NVMe targets: %w", err)
	}

	return nil
}
