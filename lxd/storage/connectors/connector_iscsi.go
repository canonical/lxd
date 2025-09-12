package connectors

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

const (
	// Status code 15 (ISCSI_ERR_SESS_EXISTS) indicates that
	// the connection is already established.
	iscsiErrCodeSessionExists = 15

	// Status code 21 (ISCSI_ERR_NO_OBJS_FOUND) indicates that
	// the no matching record, target, session, or portal was found
	// to execute the operation on.
	iscsiErrCodeNotFound = 21
)

var _ Connector = &connectorISCSI{}

type connectorISCSI struct {
	common

	iqn string
}

// ISCSIDiscoveryLogRecord represents an ISCSI discovery entry.
type ISCSIDiscoveryLogRecord struct {
	IQN string
}

// Type returns the type of the connector.
func (c *connectorISCSI) Type() string {
	return TypeISCSI
}

// Version returns the version of the iSCSI CLI (iscsiadm).
func (c *connectorISCSI) Version() (string, error) {
	// Detect and record the version of the iSCSI CLI.
	// It will fail if the "iscsiadm" is not installed on the host.
	out, err := shared.RunCommandContext(context.Background(), "iscsiadm", "--version")
	if err != nil {
		return "", fmt.Errorf("Failed to get iscsiadm version: %w", err)
	}

	fields := strings.Split(strings.TrimSpace(out), " ")
	if strings.HasPrefix(out, "iscsiadm version ") && len(fields) > 2 {
		version := fields[2] + " (iscsiadm)"
		return version, nil
	}

	return "", fmt.Errorf("Failed to get iscsiadm version: Unexpected output %q", out)
}

// LoadModules loads the iSCSI kernel modules.
// Returns true if the modules can be loaded.
func (c *connectorISCSI) LoadModules() error {
	return util.LoadModule("iscsi_tcp")
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
	lines := strings.SplitSeq(string(content), "\n")
	for line := range lines {
		iqn, ok := strings.CutPrefix(line, "InitiatorName=")
		if ok {
			c.iqn = iqn
			return iqn, nil
		}
	}

	return "", fmt.Errorf(`Failed to extract host IQN: File %q does not contain "InitiatorName"`, filename)
}

// Connect establishes a connection with the target on the given address.
func (c *connectorISCSI) Connect(ctx context.Context, targetQN string, targetAddresses ...string) (revert.Hook, error) {
	// Connects to the provided target address. If the connection is already established,
	// the session is rescanned to detect new volumes.
	connectFunc := func(ctx context.Context, s *session, targetAddr string) error {
		if s != nil && slices.Contains(s.addresses, targetAddr) {
			// If connection with the target address is already established,
			// rescan the session to ensure new volumes are detected.
			_, err := shared.RunCommandContext(ctx, "iscsiadm", "--mode", "session", "--sid", s.id, "--rescan")
			if err != nil {
				return err
			}
		}

		// Insert new iSCSI target entry into local iSCSI database.
		_, err := shared.RunCommandContext(ctx, "iscsiadm", "--mode", "node", "--targetname", targetQN, "--portal", targetAddr, "--op", "new")
		if err != nil {
			return fmt.Errorf("Failed to insert local iSCSI entries for target %q: %w", targetQN, err)
		}

		// Attempt to login into iSCSI target.
		_, err = shared.RunCommandContext(ctx, "iscsiadm", "--mode", "node", "--targetname", targetQN, "--portal", targetAddr, "--login")
		if err != nil {
			exitCode, _ := shared.ExitStatus(err)
			if exitCode == iscsiErrCodeSessionExists {
				// Nothing to do. Status code indicates that the connection
				// is already established.
				return nil
			}

			return fmt.Errorf("Failed to connect to target %q on %q via iSCSI: %w", targetQN, targetAddr, err)
		}

		return nil
	}

	return connect(ctx, c, targetQN, targetAddresses, connectFunc)
}

// Disconnect terminates a connection with the target.
func (c *connectorISCSI) Disconnect(targetQN string) error {
	// Find an existing iSCSI session.
	session, err := c.findSession(targetQN)
	if err != nil {
		return err
	}

	// Disconnect from the iSCSI target if there is an existing session.
	if session != nil {
		// Do not pass a cancelable context as the operation is relatively short
		// and most importantly we do not want to "partially" disconnect from
		// the target - potentially leaving some unclosed sessions.
		_, err = shared.RunCommandContext(context.Background(), "iscsiadm", "--mode", "node", "--targetname", targetQN, "--logout")
		if err != nil {
			exitCode, _ := shared.ExitStatus(err)
			if exitCode == iscsiErrCodeNotFound {
				// Nothing to do. Status code indicates that the session
				// was not found. This just prevents an error in case the
				// disconnect is called multiple times on the same target.
				return nil
			}

			return fmt.Errorf("Failed disconnecting from iSCSI target %q: %w", targetQN, err)
		}

		// Remove target entries from local iSCSI database.
		_, err = shared.RunCommandContext(context.Background(), "iscsiadm", "--mode", "node", "--targetname", targetQN, "--op", "delete")
		if err != nil {
			return fmt.Errorf("Failed to remove local iSCSI entries for target %q: %w", targetQN, err)
		}
	}

	return nil
}

// findSession returns an active iSCSI session that matches the given targetQN.
// If the session is not found, nil session is returned.
//
// This function first searches for checks for an existing session matching the
// provided target IQN in "/sys/class/iscsi_session". If the session is found,
// it retrieves the addresses of the active connections from
// "/sys/class/iscsi_connection".
func (c *connectorISCSI) findSession(targetQN string) (*session, error) {
	// Base path for iSCSI sessions.
	sessionBasePath := "/sys/class/iscsi_session"

	// Retrieve list of existing iSCSI sessions.
	sessions, err := os.ReadDir(sessionBasePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No active sessions.
			return nil, nil
		}

		return nil, fmt.Errorf("Failed getting a list of existing iSCSI sessions: %w", err)
	}

	sessionID := ""
	for _, session := range sessions {
		// Get the target IQN of the iSCSI session.
		iqnBytes, err := os.ReadFile(filepath.Join(sessionBasePath, session.Name(), "targetname"))
		if err != nil {
			return nil, fmt.Errorf("Failed getting the target IQN for session %q: %w", session, err)
		}

		sessionIQN := strings.TrimSpace(string(iqnBytes))
		if targetQN == sessionIQN {
			// Session found.
			sessionID = strings.TrimPrefix(session.Name(), "session")
			break
		}
	}

	if sessionID == "" {
		// No active session found.
		return nil, nil
	}

	session := &session{
		id:       sessionID,
		targetQN: targetQN,
	}

	connBasePath := "/sys/class/iscsi_connection"

	// Retrieve list of active conns for the session.
	conns, err := os.ReadDir(connBasePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No active connections.
			return session, nil
		}

		return nil, fmt.Errorf("Failed getting a list of existing iSCSI connections: %w", err)
	}

	// Iterate over active connections that correspond to the found session
	// and extract their addresses.
	connID := "connection" + sessionID
	for _, conn := range conns {
		if !strings.HasPrefix(conn.Name(), connID) {
			// Connection does not belong to the session.
			continue
		}

		// Get address of an active iSCSI connection.
		addrPath := filepath.Join(connBasePath, conn.Name(), "address")
		addrBytes, err := os.ReadFile(addrPath)
		if err != nil {
			// In case of an error when reading the address, simply skip this address.
			// We detect addresses just to reduce the number of connection attempts.
			continue
		}

		addr := strings.TrimSpace(string(addrBytes))
		session.addresses = append(session.addresses, addr)
	}

	return session, nil
}

// Discover returns the targets found on the first reachable targetAddr.
func (c *connectorISCSI) Discover(ctx context.Context, targetAddresses ...string) ([]any, error) {
	if c.Type() != TypeISCSI {
		return nil, errors.New("Discover() helper can only be used with iSCSI connector type")
	}

	// Set a deadline for the overall discovery.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result := make([]any, 0)
	for _, targetAddr := range targetAddresses {
		stdout, err := shared.RunCommandContext(ctx, "iscsiadm", "--mode", "discovery", "--type", "sendtargets", "--portal", targetAddr)
		if err != nil {
			logger.Warn("Failed connecting to discovery target", logger.Ctx{"target_address": targetAddr, "err": err})
			continue
		}

		scanner := bufio.NewScanner(strings.NewReader(stdout))

		for scanner.Scan() {
			// each string looks like "192.168.168.1:3260,41 iqn.2023-24.com.org:cz2e123asd"
			fields := strings.Fields(scanner.Text())

			if len(fields) != 2 {
				continue
			}

			if !strings.HasPrefix(fields[0], targetAddr) {
				continue
			}

			result = append(result, ISCSIDiscoveryLogRecord{
				IQN: fields[1],
			})
		}

		if len(result) != 0 {
			// We have already found something.
			break
		}
	}

	// In case none of the target addresses returned any log records also return an error.
	if len(result) == 0 {
		return nil, errors.New("Failed to fetch a discovery log record from any of the target addresses")
	}

	return result, nil
}
