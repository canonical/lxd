package connectors

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
)

var _ Connector = &connectorISCSI{}

type connectorISCSI struct {
	common

	iqn string
}

// Type returns the type of the connector.
func (c *connectorISCSI) Type() string {
	return TypeISCSI
}

// Version returns the version of the iSCSI CLI (iscsiadm).
func (c *connectorISCSI) Version() (string, error) {
	// Detect and record the version of the iSCSI CLI.
	// It will fail if the "iscsiadm" is not installed on the host.
	out, err := shared.RunCommand("iscsiadm", "--version")
	if err != nil {
		return "", fmt.Errorf("Failed to get iscsiadm version: %w", err)
	}

	fields := strings.Split(strings.TrimSpace(out), " ")
	if strings.HasPrefix(out, "iscsiadm version ") && len(fields) > 2 {
		version := fmt.Sprintf("%s (iscsiadm)", fields[2])
		return version, nil
	}

	return "", fmt.Errorf("Failed to get iscsiadm version: Unexpected output %q", out)
}

// LoadModules loads the iSCSI kernel modules.
// Returns true if the modules can be loaded.
func (c *connectorISCSI) LoadModules() bool {
	return util.LoadModule("iscsi_tcp") == nil
}

// QualifiedName returns the unique iSCSI Qualified Name (IQN) of the host.
func (c *connectorISCSI) QualifiedName() (string, error) {
	if c.iqn != "" {
		return c.iqn, nil
	}

	// Get the unique iSCSI Qualified Name (IQN) of the host. The iscsiadm
	// does not allow providing the IQN directly, so we need to extract it
	// from the /etc/iscsi/initiatorname.iscsi file on the host.
	filename := shared.HostPath("/etc/iscsi/initiatorname.iscsi")
	if !shared.PathExists(filename) {
		return "", fmt.Errorf("Failed to extract host IQN: File %q does not exist", filename)
	}

	content, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}

	// Find the IQN line in the file.
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		iqn, ok := strings.CutPrefix(line, "InitiatorName=")
		if ok {
			c.iqn = iqn
			return iqn, nil
		}
	}

	return "", fmt.Errorf(`Failed to extract host IQN: File %q does not contain "InitiatorName"`, filename)
}

// SessionID returns the ID of an iSCSI session that corresponds
// to the server's qualified name (IQN). If the session is not found,
// an empty string is returned.
func (c *connectorISCSI) SessionID(targetQN string) (string, error) {
	// Base path for iSCSI sessions.
	basePath := "/sys/class/iscsi_session"

	// Retrieve list of existing iSCSI sessions.
	sessions, err := os.ReadDir(basePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No active sessions.
			return "", nil
		}

		return "", fmt.Errorf("Failed getting a list of existing iSCSI sessions: %w", err)
	}

	for _, session := range sessions {
		// Get the target IQN of the iSCSI session.
		iqnBytes, err := os.ReadFile(filepath.Join(basePath, session.Name(), "targetname"))
		if err != nil {
			return "", fmt.Errorf("Failed getting the target IQN for session %q: %w", session, err)
		}

		sessionIQN := strings.TrimSpace(string(iqnBytes))
		sessionID := strings.TrimPrefix(session.Name(), "session")

		if targetQN == sessionIQN {
			// Already connected.
			return sessionID, nil
		}
	}

	return "", nil
}

// discoverTargets discovers the available iSCSI targets on a given address.
func (c *connectorISCSI) discoverTargets(ctx context.Context, targetAddr string) error {
	// Discover the available iSCSI targets on a given address.
	_, _, err := shared.RunCommandSplit(ctx, nil, nil, "iscsiadm", "--mode", "discovery", "--type", "sendtargets", "--portal", targetAddr)
	if err != nil {
		return fmt.Errorf("Failed to discover available iSCSI targets on %q: %w", targetAddr, err)
	}

	return nil
}

// Connect establishes a connection with the target on the given address.
func (c *connectorISCSI) Connect(ctx context.Context, targetAddr string, targetQN string) error {
	// Try to find an existing iSCSI session.
	sessionID, err := c.SessionID(targetQN)
	if err != nil {
		return err
	}

	if sessionID != "" {
		// Already connected.
		// Rescan the session to ensure new volumes are detected.
		_, err := shared.RunCommand("iscsiadm", "--mode", "session", "--sid", sessionID, "--rescan")
		if err != nil {
			return err
		}

		return nil
	}

	err = c.discoverTargets(ctx, targetAddr)
	if err != nil {
		return err
	}

	// Attempt to login into iSCSI target.
	_, stderr, err := shared.RunCommandSplit(ctx, nil, nil, "iscsiadm", "--mode", "node", "--targetname", targetQN, "--portal", targetAddr, "--login")
	if err != nil {
		return fmt.Errorf("Failed to connect to target %q on %q via iSCSI: %w", targetQN, targetAddr, err)
	}

	if stderr != "" {
		return fmt.Errorf("Failed to connect to target %q on %q via iSCSI: %s", targetQN, targetAddr, stderr)
	}

	return nil
}

// ConnectAll establishes a connection with all targets available on the given address.
func (c *connectorISCSI) ConnectAll(ctx context.Context, targetAddr string) error {
	err := c.discoverTargets(ctx, targetAddr)
	if err != nil {
		return err
	}

	// Attempt to login into all iSCSI targets.
	_, stderr, err := shared.RunCommandSplit(ctx, nil, nil, "iscsiadm", "--mode", "node", "--portal", targetAddr, "--login")
	if err != nil {
		return fmt.Errorf("Failed to connect any target on %q via iSCSI: %w", targetAddr, err)
	}

	if stderr != "" {
		return fmt.Errorf("Failed to connect any target on %q via iSCSI: %s", targetAddr, stderr)
	}

	return fmt.Errorf("ConnectAll not implemented")
}

// Disconnect terminates a connection with the target.
func (c *connectorISCSI) Disconnect(targetQN string) error {
	// Find an existing iSCSI session.
	sessionID, err := c.SessionID(targetQN)
	if err != nil {
		return err
	}

	// Disconnect from the iSCSI target if there is an existing session.
	if sessionID != "" {
		_, err := shared.RunCommand("iscsiadm", "--mode", "node", "--targetname", targetQN, "--logout")
		if err != nil {
			return fmt.Errorf("Failed disconnecting from iSCSI target %q: %w", targetQN, err)
		}
	}

	return nil
}

// DisconnectAll terminates all connections with all targets.
func (c *connectorISCSI) DisconnectAll() error {
	_, err := shared.RunCommand("iscsiadm", "--mode", "node", "--logoutall", "all")
	if err != nil {
		return fmt.Errorf("Failed disconnecting from iSCSI targets: %w", err)
	}

	return nil
}
