package connectors

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/revert"
)

type connectorISCSI struct {
	common

	iqn atomic.Pointer[string]
}

func newConnectorISCSI(serverUUID string) (Connector, error) {
	c := &connectorISCSI{
		common: common{
			serverUUID: serverUUID,
		},
	}

	return c, nil
}

// iscsiDefaultPort is the default port number for iSCSI discovery and targets.
const iscsiDefaultPort = "3260"

// iscsiDiskDevicePrefix is the prefix of the iSCSI disk device name in /dev/disk/by-id/.
const iscsiDiskDevicePrefix = "scsi-"

const (
	// Status code 15 (ISCSI_ERR_SESS_EXISTS) indicates that
	// the connection is already established.
	iscsiErrCodeSessionExists = 15

	// Status code 21 (ISCSI_ERR_NO_OBJS_FOUND) indicates that
	// the no matching record, target, session, or portal was found
	// to execute the operation on.
	iscsiErrCodeNotFound = 21
)

// Type returns the type of the connector.
func (c *connectorISCSI) Type() ConnectorType {
	return TypeISCSI
}

// Version returns the version of the iSCSI CLI (iscsiadm).
func (c *connectorISCSI) Version() (string, error) {
	// Detect and record the version of the iSCSI CLI.
	// It will fail if the "iscsiadm" is not installed on the host.
	out, err := shared.RunCommand(context.Background(), "iscsiadm", "--version")
	if err != nil {
		return "", fmt.Errorf("Failed getting iscsiadm version: %w", err)
	}

	fields := strings.Split(strings.TrimSpace(out), " ")
	if strings.HasPrefix(out, "iscsiadm version ") && len(fields) > 2 {
		version := fields[2] + " (iscsiadm)"
		return version, nil
	}

	return "", fmt.Errorf("Failed getting iscsiadm version: Unexpected output %q", out)
}

// LoadModules loads the iSCSI kernel modules. Returns nil error if
// the modules can be loaded.
func (c *connectorISCSI) LoadModules() error {
	return util.LoadModule("iscsi_tcp")
}

// QualifiedName returns the unique iSCSI Qualified Name (IQN) of the host.
func (c *connectorISCSI) QualifiedName() (string, error) {
	iqnPtr := c.iqn.Load()
	if iqnPtr != nil && *iqnPtr != "" {
		return *iqnPtr, nil
	}

	// Get the unique iSCSI Qualified Name (IQN) of the host. The iscsiadm
	// does not allow providing the IQN directly, so we need to extract it
	// from the /etc/iscsi/initiatorname.iscsi file on the host.
	filename := shared.HostPath("/etc/iscsi/initiatorname.iscsi")
	content, err := os.ReadFile(filename)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("Failed extracting host IQN: File %q does not exist", filename)
		}

		return "", err
	}

	// Find the IQN line in the file.
	lines := strings.SplitSeq(string(content), "\n")
	for line := range lines {
		iqn, ok := strings.CutPrefix(line, "InitiatorName=")
		if ok {
			c.iqn.Store(&iqn)
			return iqn, nil
		}
	}

	return "", fmt.Errorf(`Failed extracting host IQN: File %q does not contain "InitiatorName"`, filename)
}

// Discover returns the targets found on one of the discovery addresses.
func (c *connectorISCSI) Discover(ctx context.Context, discoveryAddresses ...string) ([]Target, error) {
	discoverOperation := func(ctx context.Context, discoveryAddress string) ([]Target, error) {
		stdout, err := shared.RunCommand(ctx, "iscsiadm",
			"--mode", "discovery",
			"--type", "sendtargets",
			"--portal", discoveryAddress,
		)
		if err != nil {
			return nil, fmt.Errorf("Failed connecting to discovery endpoint %q: %w", discoveryAddress, err)
		}

		var log []Target
		scanner := bufio.NewScanner(strings.NewReader(stdout))
		for scanner.Scan() {
			// each string looks like "192.168.168.1:3260,41 iqn.2023-24.com.org:cz2e123asd"

			addrAndTag, iqn, ok := strings.Cut(scanner.Text(), " ")
			if !ok {
				// Skip non target entries.
				continue
			}

			addr, _, ok := strings.Cut(addrAndTag, ",")
			if !ok {
				// Skip non target entries.
				continue
			}

			// Make sure addr have a port number for stable output.
			addr = shared.EnsurePort(addr, iscsiDefaultPort)

			log = append(log, Target{QualifiedName: iqn, Address: addr})
		}

		return log, nil
	}

	// Make sure the provided addresses are unique and in an uniform format.
	discoveryAddresses = shared.Unique(slices.Clone(discoveryAddresses))
	for i := range discoveryAddresses {
		discoveryAddresses[i] = shared.EnsurePort(discoveryAddresses[i], iscsiDefaultPort)
	}

	return discover(ctx, discoverOperation, discoveryAddresses...)
}

// Connect establishes connections to targets.
func (c *connectorISCSI) Connect(ctx context.Context, targets ...Target) (revert.Hook, error) {
	// Find an existing iSCSI sessions matching the provided targets.
	sessions, err := iscsiSessions(targets...)
	if err != nil {
		return nil, err
	}

	connectOperation := func(ctx context.Context, target Target) error {
		session, hasSession := sessions.ForTarget(target)
		if hasSession {
			// If connection with the target address is already established, rescan
			// the session to ensure new volumes are detected.
			_, err := shared.RunCommand(ctx, "iscsiadm",
				"--mode", "session",
				"--sid", session.ID,
				"--rescan",
			)
			if err != nil {
				return fmt.Errorf("Failed refreshing session %q for target %q [%s]: %w", session.ID, target.QualifiedName, target.Address, err)
			}
		}

		// Insert new iSCSI target entry into local iSCSI database.
		_, err = shared.RunCommand(ctx, "iscsiadm",
			"--mode", "node",
			"--targetname", target.QualifiedName,
			"--portal", target.Address,
			"--op", "new",
		)
		if err != nil {
			return fmt.Errorf("Failed inserting local iSCSI entries for target %q [%s]: %w", target.QualifiedName, target.Address, err)
		}

		// Attempt to login into iSCSI target.
		_, err = shared.RunCommand(ctx, "iscsiadm",
			"--mode", "node",
			"--targetname", target.QualifiedName,
			"--portal", target.Address,
			"--login",
		)
		if err != nil {
			exitCode, _ := shared.ExitStatus(err)
			if exitCode == iscsiErrCodeSessionExists {
				// Nothing to do. Status code indicates that the connection is already
				// established.
				return nil
			}

			return fmt.Errorf("Failed connecting to target %q [%s] via iSCSI: %w", target.QualifiedName, target.Address, err)
		}

		return nil
	}

	revert, err := connect(ctx, connectOperation, targets...)
	if err != nil && sessions.Len() == 0 {
		// On failure, if no session existed before the connect call, attempt to
		// restore the system state.
		_ = c.Disconnect(ctx, targets...)
	}

	return revert, err
}

// Disconnect terminates connections to targets.
func (c *connectorISCSI) Disconnect(_ context.Context, targets ...Target) error {
	// Find an existing iSCSI sessions matching the provided targets.
	sessions, err := iscsiSessions(targets...)
	if err != nil {
		return err
	}

	disconnectOperation := func(ctx context.Context, target Target) error {
		session, has := sessions.ForIQN(target.QualifiedName)
		if !has {
			// No existing session - nothing to do.
			return nil
		}

		// iscsiadm do not supports disconnecting individual targets, rather all
		// targets using the same IQN are disconnected. This should ne be an issue
		// for correctly configured systems. However since disconnection invocation
		// are shared by targets with the same IQN wee need to ensure sequential
		// execution.
		unlock, err := lockQualifiedName(ctx, session.IQN)
		if err != nil {
			return fmt.Errorf("Failed disconnecting from target %q [%s] due to the IQN lock acquisition failure: %w", target.QualifiedName, target.Address, err)
		}

		defer unlock()

		_, err = shared.RunCommand(ctx, "iscsiadm",
			"--mode", "node",
			"--targetname", session.IQN,
			"--logout",
		)
		if err != nil {
			exitCode, _ := shared.ExitStatus(err)
			if exitCode == iscsiErrCodeNotFound {
				// Nothing to do. Status code indicates that the session
				// was not found. This just prevents an error in case the
				// disconnect is called multiple times on the same target.
				return nil
			}

			return fmt.Errorf("Failed disconnecting from iSCSI IQN %q: %w", session.IQN, err)
		}

		// Remove target entries from local iSCSI database.
		_, err = shared.RunCommand(ctx, "iscsiadm",
			"--mode", "node",
			"--targetname", session.IQN,
			"--op", "delete",
		)
		if err != nil {
			return fmt.Errorf("Failed removing local iSCSI entries for IQN %q: %w", session.IQN, err)
		}

		return nil
	}

	// Do not pass a cancelable context as the operation is relatively short and
	// most importantly we do not want to "partially" disconnect from targets,
	// potentially leaving some unclosed sessions.
	return disconnect(context.Background(), disconnectOperation, targets...)
}

// GetDiskDevicePath retrieves the path of the mapped iSCSI.
//
// When wait is false function performs a simple retrieval.
//
// When wait is true function additionally waits for the mapped iSCSI device
// to appear. If the device is not a multipath device, multipath is forced and
// the device path is looked up again. An error is returned if no multipath
// device is found after that.
func (c *connectorISCSI) GetDiskDevicePath(ctx context.Context, wait bool, diskNameFilter block.DeviceNameFilterFunc) (string, error) {
	if diskNameFilter == nil {
		diskNameFilter = func(diskPath string) bool { return true }
	}

	diskPathFilterWithPrefix := func(diskPath string) bool {
		return strings.HasPrefix(diskPath, iscsiDiskDevicePrefix) && diskNameFilter(diskPath)
	}

	if !wait {
		return c.common.GetDiskDevicePath(ctx, wait, diskPathFilterWithPrefix)
	}

	ctx, cancel := shared.WithDefaultTimeout(ctx, 30*time.Second)
	defer cancel()

	devicePath, err := c.common.GetDiskDevicePath(ctx, wait, diskPathFilterWithPrefix)
	if err != nil {
		return "", err
	}

	if isMultipathDevice(devicePath) {
		return devicePath, nil
	}

	// Device is not a multipath device.
	// Create multipath device from a found device path.
	_, err = shared.RunCommand(ctx, "multipath", devicePath)
	if err != nil {
		return "", fmt.Errorf("Failed configuring multipath for device %q: %w", devicePath, err)
	}

	// Filter that makes sure the found device resolves to a multipath device.
	multipathDeviceFiler := func(devicePath string) bool {
		if !diskPathFilterWithPrefix(devicePath) {
			return false
		}

		path, err := filepath.EvalSymlinks(devicePath)
		if err != nil {
			return false
		}

		return isMultipathDevice(path)
	}

	// The multipath command is synchronous, but udev updates the /dev/disk/by-id
	// symlinks asynchronously. Wait for the multipath-backed device path to appear.
	return c.common.GetDiskDevicePath(ctx, wait, multipathDeviceFiler)
}

// WaitDiskDeviceResize waits until the disk device reflects the new size.
// For iSCSI multipath device, the device-mapper is refreshed before waiting for the new size.
func (c *connectorISCSI) WaitDiskDeviceResize(ctx context.Context, devicePath string, newSizeBytes int64) error {
	ctx, cancel := shared.WithDefaultTimeout(ctx, 30*time.Second)
	defer cancel()

	if isMultipathDevice(devicePath) {
		// Ask multipathd to refresh multipath device size.
		_, err := shared.RunCommand(ctx, "multipath", "-r", devicePath)
		if err != nil {
			return fmt.Errorf("Failed updating multipath device %q size: %w", devicePath, err)
		}
	}

	return c.common.WaitDiskDeviceResize(ctx, devicePath, newSizeBytes)
}

func isMultipathDevice(devicePath string) bool {
	return strings.HasPrefix(filepath.Base(devicePath), "dm-")
}

// RemoveDiskDevice removes the disk device from the system.
//
// When iSCSI volume is disconnected from the host, the device remains on
// the system. The device can be removed either manually, or automatically when
// disconnecting from the iSCSI session. However, logging out of the session is
// not desired as it would disconnect all connected volumes. Therefore, this
// function manually removes the device, preserving other connected volumes.
//
// Note that iSCSI device should be removed from the host before being unmapped
// on the storage array side. On some storage arrays (for example, HPE Alletra
// and Pure) we've seen that removing a vLUN from the array immediately makes
// device inaccessible and traps any task that tries to access it to D-state
// (and this task can be systemd-udevd which tries to remove a device node!).
// That's why it is better to remove the device node from the host and then
// remove vLUN.
func (c *connectorISCSI) RemoveDiskDevice(ctx context.Context, devicePath string) error {
	if devicePath == "" {
		return nil
	}

	// removeDevice removes device from the system if the device is removable.
	removeDevice := func(devName string) error {
		path := "/sys/block/" + devName + "/device/delete"

		err := os.WriteFile(path, []byte("1"), 0400)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}

		return nil
	}

	deviceName := filepath.Base(devicePath)

	if isMultipathDevice(devicePath) {
		// Collect the slave devices before removing the multipath map,
		// as /sys/block/dm-X/slaves/ will be gone after removal.
		slavesPath := filepath.Join("/sys/block", deviceName, "slaves")
		slaves, _ := os.ReadDir(slavesPath)

		// Remove the multipath map.
		//
		// This may fail transiently with "map in use" if the device is still
		// briefly open (for example by udev), so retry a few times before giving up.
		var err error
		for range 10 {
			ctxErr := ctx.Err()
			if ctxErr != nil {
				// Preserve the command error if we already have one.
				// Otherwise return the generic context error.
				if err == nil {
					err = ctxErr
				}

				break
			}

			_, err = shared.RunCommand(ctx, "multipath", "-f", devicePath)
			if err == nil {
				break
			}

			time.Sleep(500 * time.Millisecond)
		}

		if err != nil {
			return fmt.Errorf("Failed removing multipath device %q: %w", devicePath, err)
		}

		// Remove the underlying SCSI devices that were part of the multipath map.
		// If not removed, they remain on the system and cause I/O errors when the
		// volume is disconnected from the storage array.
		for _, slave := range slaves {
			err := removeDevice(slave.Name())
			if err != nil {
				return fmt.Errorf("Failed removing multipath slave device %q: %w", slave.Name(), err)
			}
		}
	} else {
		// For non-multipath device (/dev/sd*), remove the device itself.
		err := removeDevice(deviceName)
		if err != nil {
			return fmt.Errorf("Failed removing device %q: %w", devicePath, err)
		}
	}

	// Wait until the device has disappeared.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if !block.WaitDiskDeviceGone(ctx, devicePath) {
		return fmt.Errorf("Timeout exceeded waiting for device %q to disappear", devicePath)
	}

	return nil
}

// iscsiSessionsSet set of iSCSI sessions.
type iscsiSessionsSet struct {
	byIQN    map[string]*iscsiSession
	byTarget map[Target]*iscsiSession
}

// Add adds the given session to the set.
func (set *iscsiSessionsSet) Add(session iscsiSession) {
	if set.byIQN == nil {
		set.byIQN = map[string]*iscsiSession{}
	}

	if set.byTarget == nil {
		set.byTarget = map[Target]*iscsiSession{}
	}

	set.byIQN[session.IQN] = &session
	for _, addr := range session.Addresses {
		set.byTarget[Target{QualifiedName: session.IQN, Address: addr}] = &session
	}
}

// Len returns total number of iSCSI sessions in the set.
func (set iscsiSessionsSet) Len() int {
	return len(set.byIQN)
}

// ForIQN retrieves session associated with the provided IQN from the set, if any.
func (set iscsiSessionsSet) ForIQN(iqn string) (iscsiSession, bool) {
	session, ok := set.byIQN[iqn]
	return *session, ok
}

// ForTarget retrieves session associated with the provided target from the set, if any.
func (set iscsiSessionsSet) ForTarget(target Target) (iscsiSession, bool) {
	session, ok := set.byTarget[target]
	return *session, ok
}

// iscsiSession represents an iSCSI connector specific session details.
type iscsiSession struct {
	ID        string
	IQN       string
	Addresses []string
}

const (
	iscsiSessionsPath        = "/sys/class/iscsi_session"
	iscsiSessionDirPrefix    = "session"
	iscsiConnectionDirPrefix = "connection"
)

// iscsiSessions returns active iSCSI sessions associated with the provided
// targets or their qualified names.
//
// This function first searches for existing sessions which IQNs match
// qualified name of any of the provided targets. Then ranges over them
// and retrieves matching addresses.
func iscsiSessions(targets ...Target) (iscsiSessionsSet, error) {
	sessionIDs, err := iscsiSessionsIDs()
	if err != nil {
		return iscsiSessionsSet{}, err
	}

	// Range over all known sessions.
	sessions := iscsiSessionsSet{}
	iqns := targetsQualifiedNames(targets...)
	for _, sessionID := range sessionIDs {
		iqn, err := iscsiSessionIQN(sessionID)
		if err != nil {
			return iscsiSessionsSet{}, err
		}

		if iqn == "" {
			// Session was just removed.
			continue
		}

		if !slices.Contains(iqns, iqn) {
			// Session is not related to any of the specified IQNs.
			continue
		}

		addresses, err := iscsiAddressesInSession(sessionID)
		if err != nil {
			return iscsiSessionsSet{}, err
		}

		sessions.Add(iscsiSession{
			ID:        sessionID,
			IQN:       iqn,
			Addresses: addresses,
		})
	}

	return sessions, nil
}

// iscsiSessionsIDs retrieves a list of all sessions IDs in the system.
func iscsiSessionsIDs() ([]string, error) {
	sessionsDirs, err := os.ReadDir(iscsiSessionsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No active sessions.
			return nil, nil
		}

		return nil, fmt.Errorf("Failed getting a list of existing iSCSI sessions: %w", err)
	}

	sessionsIDs := make([]string, 0, len(sessionsDirs))
	for _, sessionDir := range sessionsDirs {
		sessionID, ok := strings.CutPrefix(sessionDir.Name(), iscsiSessionDirPrefix)
		if !ok {
			continue
		}

		sessionsIDs = append(sessionsIDs, sessionID)
	}

	return sessionsIDs, nil
}

// iscsiSessionIQN retrieves IQN associated with a session with the given ID.
func iscsiSessionIQN(sessionID string) (string, error) {
	targetNamePath := filepath.Join(iscsiSessionsPath, iscsiSessionDirPrefix+sessionID, "targetname")
	iqnBytes, err := os.ReadFile(targetNamePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Session do not exists.
			return "", nil
		}

		return "", fmt.Errorf("Failed getting an IQN of the existing iSCSI sessions (%q): %w", sessionID, err)
	}

	return string(bytes.TrimSpace(iqnBytes)), nil
}

// iscsiAddressesInSession retrieves addresses associated with a session with the given ID.
func iscsiAddressesInSession(sessionID string) ([]string, error) {
	sessionDirPath := filepath.Join(iscsiSessionsPath, iscsiSessionDirPrefix+sessionID)
	entries, err := os.ReadDir(sessionDirPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Session do not exists.
			return nil, nil
		}

		return nil, fmt.Errorf("Failed getting a list of addresses of the existing iSCSI sessions (%q): %w", sessionID, err)
	}

	var addresses []string
	for _, entry := range entries {
		connectionID, ok := strings.CutPrefix(entry.Name(), iscsiConnectionDirPrefix)
		if !ok {
			// Not a connection directory.
			continue
		}

		address, err := iscsiAddressOfConnection(sessionID, connectionID)
		if err != nil {
			return nil, err
		}

		if address == "" {
			// Connection or the entire session was just removed.
			continue
		}

		addresses = append(addresses, address)
	}

	return addresses, nil
}

// iscsiAddressOfConnection retrieves address associated with a connection with
// the given connection ID within a session with the given session ID.
func iscsiAddressOfConnection(sessionID, connectionID string) (string, error) {
	addressPath := filepath.Join(iscsiSessionsPath, iscsiSessionDirPrefix+sessionID, iscsiConnectionDirPrefix+connectionID, "address")
	addressBytes, err := os.ReadFile(addressPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Session or connection do not exists or was just removed.
			return "", nil
		}

		return "", fmt.Errorf("Failed getting an address of the existing connection (%q) within iSCSI sessions (%q): %w", connectionID, sessionID, err)
	}

	portPath := filepath.Join(iscsiSessionsPath, iscsiSessionDirPrefix+sessionID, iscsiConnectionDirPrefix+connectionID, "port")
	portBytes, err := os.ReadFile(portPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Session or connection do not exists or was just removed.
			return "", nil
		}

		return "", fmt.Errorf("Failed getting an address of the existing connection (%q) within iSCSI sessions (%q): %w", connectionID, sessionID, err)
	}

	return net.JoinHostPort(string(bytes.TrimSpace(addressBytes)), string(bytes.TrimSpace(portBytes))), nil
}
