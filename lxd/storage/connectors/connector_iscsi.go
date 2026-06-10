package connectors

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/storage/block"
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

// scsiDiskDevicePrefix is the prefix of the SCSI disk device name in /dev/disk/by-id/.
const scsiDiskDevicePrefix = "scsi-"

const (
	// ISCSIDefaultPort is the default port number for iSCSI targets.
	ISCSIDefaultPort = "3260"
)

var _ Connector = &connectorISCSI{}

type connectorISCSI struct {
	common

	iqn string
}

// ISCSIDiscoveryLogRecord represents an ISCSI discovery entry.
type ISCSIDiscoveryLogRecord struct {
	Address        string
	PortalGroupTag string
	IQN            string
}

// Type returns the type of the connector.
func (c *connectorISCSI) Type() string {
	return TypeISCSI
}

// Transport returns the transport type of the connector.
func (c *connectorISCSI) Transport() TransportType {
	return TransportTCP
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
			c.iqn = iqn
			return iqn, nil
		}
	}

	return "", fmt.Errorf(`Failed extracting host IQN: File %q does not contain "InitiatorName"`, filename)
}

// Connect establishes a connection with the target on the given address.
func (c *connectorISCSI) Connect(ctx context.Context, targetQN string, targetAddresses ...string) (revert.Hook, error) {
	// Connects to the provided target address. If the connection is already established,
	// the session is rescanned to detect new volumes.
	connectFunc := func(ctx context.Context, s *session, targetAddr string) error {
		targetAddr = shared.EnsurePort(targetAddr, ISCSIDefaultPort)
		if s != nil && slices.Contains(s.addresses, targetAddr) {
			// If connection with the target address is already established,
			// rescan the session to ensure new volumes are detected.
			_, err := shared.RunCommand(ctx, "iscsiadm", "--mode", "session", "--sid", s.id, "--rescan")
			if err != nil {
				return err
			}
		}

		// Insert new iSCSI target entry into local iSCSI database.
		_, err := shared.RunCommand(ctx, "iscsiadm", "--mode", "node", "--targetname", targetQN, "--portal", targetAddr, "--op", "new")
		if err != nil {
			return fmt.Errorf("Failed inserting local iSCSI entries for target %q: %w", targetQN, err)
		}

		// Attempt to login into iSCSI target.
		_, err = shared.RunCommand(ctx, "iscsiadm", "--mode", "node", "--targetname", targetQN, "--portal", targetAddr, "--login")
		if err != nil {
			exitCode, _ := shared.ExitStatus(err)
			if exitCode == iscsiErrCodeSessionExists {
				// Nothing to do. Status code indicates that the connection
				// is already established.
				return nil
			}

			return fmt.Errorf("Failed connecting to target %q on %q via iSCSI: %w", targetQN, targetAddr, err)
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
		_, err = shared.RunCommand(context.Background(), "iscsiadm", "--mode", "node", "--targetname", targetQN, "--logout")
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
		_, err = shared.RunCommand(context.Background(), "iscsiadm", "--mode", "node", "--targetname", targetQN, "--op", "delete")
		if err != nil {
			return fmt.Errorf("Failed removing local iSCSI entries for target %q: %w", targetQN, err)
		}
	}

	return nil
}

// findSession returns an active iSCSI session that matches the given targetQN.
// If the session is not found, nil session is returned.
//
// This function first searches for an existing session matching the
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

		// Get port of an active iSCSI connection.
		portPath := filepath.Join(connBasePath, conn.Name(), "port")
		portBytes, err := os.ReadFile(portPath)
		if err != nil {
			// In case of an error when reading the port, use default port.
			portBytes = []byte(ISCSIDefaultPort)
		}

		addr := net.JoinHostPort(strings.TrimSpace(string(addrBytes)), strings.TrimSpace(string(portBytes)))
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
		targetAddr = shared.EnsurePort(targetAddr, ISCSIDefaultPort)
		stdout, err := shared.RunCommand(ctx, "iscsiadm", "--mode", "discovery", "--type", "sendtargets", "--portal", targetAddr, "--op", "nonpersistent")
		if err != nil {
			logger.Warn("Failed connecting to discovery target", logger.Ctx{"target_address": targetAddr, "err": err})
			continue
		}

		scanner := bufio.NewScanner(strings.NewReader(stdout))

		for scanner.Scan() {
			// each string looks like "192.168.168.1:3260,41 iqn.2023-24.com.org:cz2e123asd"

			// Skip invalid entries.
			addrAndTag, iqn, ok := strings.Cut(strings.TrimSpace(scanner.Text()), " ")
			if !ok {
				continue
			}

			// Skip invalid entries.
			addr, tag, ok := strings.Cut(addrAndTag, ",")
			if !ok {
				continue
			}

			// Make sure addr has a port number for stable output.
			addr = shared.EnsurePort(addr, ISCSIDefaultPort)

			if addr != targetAddr {
				continue
			}

			result = append(result, ISCSIDiscoveryLogRecord{
				Address:        addr,
				PortalGroupTag: tag,
				IQN:            iqn,
			})
		}

		if len(result) != 0 {
			// We have already found something.
			break
		}
	}

	// In case none of the target addresses returned any log records also return an error.
	if len(result) == 0 {
		return nil, errors.New("Failed fetching a discovery log record from any of the target addresses")
	}

	return result, nil
}

// WaitDiskDevicePath waits for the mapped iSCSI device to appear.
// If the device is not a multipath device, multipath is forced and the device path is looked up again.
// An error is returned if no multipath device is found after that.
func (c *connectorISCSI) WaitDiskDevicePath(ctx context.Context, diskPathFilter block.DevicePathFilterFunc) (string, error) {
	_, ok := ctx.Deadline()
	if !ok {
		// Set a default timeout of 30 seconds for the context
		// if no deadline is already configured.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	devicePath, err := block.WaitDiskDevicePath(ctx, scsiDiskDevicePrefix, diskPathFilter)
	if err != nil {
		return "", err
	}

	if isMultipathDevice(devicePath) {
		err := waitMultipathReady(ctx, devicePath)
		if err != nil {
			return "", err
		}

		return devicePath, nil
	}

	// Device is not a multipath device.
	// Create multipath device from a found device path.
	_, err = shared.RunCommand(ctx, "multipath", devicePath)
	if err != nil {
		return "", fmt.Errorf("Failed configuring multipath for device %q: %w", devicePath, err)
	}

	// Filter that makes sure the found device resolves to a multipath device.
	multipathDeviceFilter := func(devicePath string) bool {
		if !diskPathFilter(devicePath) {
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
	mpDevicePath, err := block.WaitDiskDevicePath(ctx, scsiDiskDevicePrefix, multipathDeviceFilter)
	if err != nil {
		return "", err
	}

	err = waitMultipathReady(ctx, mpDevicePath)
	if err != nil {
		return "", err
	}

	return mpDevicePath, nil
}

// GetDiskDevicePath returns the path of the mapped iSCSI device.
func (c *connectorISCSI) GetDiskDevicePath(diskPathFilter block.DevicePathFilterFunc) (string, error) {
	return block.GetDiskDevicePath(scsiDiskDevicePrefix, diskPathFilter)
}

// RemoveDiskDevice removes the disk device from the system.
//
// When iSCSI volume is disconnected from the host, the device remains on the system.
// The device can be removed either manually, or automatically when disconnecting from the iSCSI session.
// However, logging out of the session is not desired as it would disconnect all connected volumes.
// Therefore, this function manually removes the device, preserving other connected volumes.
//
// Note that iSCSI device should be removed from the host before being unmapped on the storage array side.
// On some storage arrays (for example, HPE Alletra and Pure) we've seen that removing a vLUN from the array
// immediately makes device inaccessible and traps any task that tries to access it
// to D-state (and this task can be systemd-udevd which tries to remove a device node!).
// That's why it is better to remove the device node from the host and then remove vLUN.
func (c *connectorISCSI) RemoveDiskDevice(ctx context.Context, devicePath string) error {
	if devicePath == "" {
		return nil
	}

	// removeDevice removes device from the system if the device is removable.
	removeDevice := func(devName string) error {
		path := "/sys/block/" + devName + "/device/delete"

		err := os.WriteFile(path, []byte("1"), 0200)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}

		return nil
	}

	deviceName := filepath.Base(devicePath)

	// If the device is gone, we are done.
	if !shared.PathExists(devicePath) {
		return nil
	}

	if isMultipathDevice(devicePath) {
		slaveDevices, err := findMultipathSCSIDevices(deviceName)
		if err != nil {
			return fmt.Errorf("Failed searching SCSI paths for multipath device %q: %w", devicePath, err)
		}

		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		// Remove the multipath map.
		//
		// This may fail transiently with "map in use" if the device is still
		// briefly open (for example by udev), so retry a few times before giving up.
		var flushErr error
		for range 10 {
			_, flushErr = shared.RunCommand(ctx, "multipath", "-f", devicePath)

			// Break if the device disappeared.
			if flushErr == nil || !shared.PathExists(devicePath) {
				break
			}

			select {
			case <-ctx.Done():
				return fmt.Errorf("Timeout exceeded removing multipath device %q: %w", devicePath, ctx.Err())
			case <-ticker.C:
			}
		}

		// Only return a failure if the map still exists after our retries.
		if flushErr != nil && shared.PathExists(devicePath) {
			return fmt.Errorf("Failed removing multipath device %q: %w", devicePath, flushErr)
		}

		// Remove the underlying SCSI devices that were part of the multipath map.
		// If not removed, they remain on the system and cause I/O errors when the
		// volume is disconnected from the storage array.
		for _, devName := range slaveDevices {
			err := removeDevice(devName)
			if err != nil {
				return fmt.Errorf("Failed removing multipath slave device %q: %w", devName, err)
			}
		}

		// Wait for the underlying SCSI devices to disappear.
		for _, devName := range slaveDevices {
			devPath := filepath.Join("/sys/block", devName)
			if !block.WaitDiskDeviceGone(ctx, devPath) {
				return fmt.Errorf("Timeout exceeded waiting for multipath slave device %q to disappear", devPath)
			}
		}
	} else {
		// For non-multipath device (/dev/sd*), remove the device itself.
		err := removeDevice(deviceName)
		if err != nil {
			return fmt.Errorf("Failed removing device %q: %w", devicePath, err)
		}

		if !block.WaitDiskDeviceGone(ctx, devicePath) {
			return fmt.Errorf("Timeout exceeded waiting for device %q to disappear", devicePath)
		}
	}

	return nil
}

// WaitDiskDeviceResize waits until the disk device reflects the new size.
// For iSCSI multipath device, the device-mapper is refreshed before waiting for the new size.
func (c *connectorISCSI) WaitDiskDeviceResize(ctx context.Context, diskPath string, newSizeBytes int64) error {
	_, ok := ctx.Deadline()
	if !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	// Trigger rescan on all SCSI devices so the kernel reports the new size.
	err := rescanMultipathSCSIDevices(diskPath)
	if err != nil {
		return err
	}

	if isMultipathDevice(diskPath) {
		// Ask multipathd to refresh multipath device size.
		_, err := shared.RunCommand(ctx, "multipath", "-r", diskPath)
		if err != nil {
			return fmt.Errorf("Failed updating multipath device %q size: %w", diskPath, err)
		}
	}

	return block.WaitDiskDeviceResize(ctx, diskPath, newSizeBytes)
}

func isMultipathDevice(devicePath string) bool {
	return strings.HasPrefix(filepath.Base(devicePath), "dm-")
}

// findMultipathSCSIDevices returns every /sys/block/sd* basename that
// belongs to the multipath device-mapper device named by dmName.
//
// The returned value contains direct multipath slaves and any other device whose
// device/wwid matches the multipath device's WWID, which includes devices that were
// previously failed and dropped from the map by multipathd but still exist as kernel
// SCSI devices. This is necessary to fully clean up all SCSI paths to the device when
// removing multipath device, and avoid leaving zombie devices that trigger
// "Logical unit not supported" probe storms on the next reuse of the same WWID after
// the array detaches the underlying LUN.
//
// If the multipath WWID cannot be parsed, only direct multipath slaves are returned.
func findMultipathSCSIDevices(dmName string) ([]string, error) {
	var devices []string

	addDeviceIfNotExist := func(name string) {
		// Only collect unique device names.
		if !slices.Contains(devices, name) {
			devices = append(devices, name)
		}
	}

	normalizeWWID := func(wwid string) string {
		wwid = strings.TrimSpace(wwid)
		wwid = strings.ToLower(wwid)

		for _, prefix := range []string{"0x", "scsi-", "naa.", "eui."} {
			wwid = strings.TrimPrefix(wwid, prefix)
		}

		return wwid
	}

	// Collect direct multipath slaves from /sys/block/dmName/slaves/*.
	slavesPath := filepath.Join("/sys/block", dmName, "slaves")
	slaves, err := os.ReadDir(slavesPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("Failed reading slaves of %q: %w", dmName, err)
	}

	for _, dev := range slaves {
		addDeviceIfNotExist(dev.Name())
	}

	// Extract the WWID from the multipath UUID.
	mpUUIDBytes, err := os.ReadFile(filepath.Join("/sys/block", dmName, "dm", "uuid"))
	if err != nil {
		// No WWID found for multipath device.
		return devices, nil
	}

	// Trim "mpath-" prefix from UUID to get the WWID.
	mpUUID := strings.ToLower(strings.TrimSpace(string(mpUUIDBytes)))
	mpUUID, ok := strings.CutPrefix(mpUUID, "mpath-")
	if !ok {
		return devices, nil
	}

	mpWWID := normalizeWWID(mpUUID)
	if mpWWID == "" {
		return devices, nil
	}

	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil, fmt.Errorf("Failed reading /sys/block: %w", err)
	}

	// Look for any /sys/block/sd* whose device/wwid matches the multipath WWID.
	for _, dev := range entries {
		if !strings.HasPrefix(dev.Name(), "sd") {
			continue
		}

		devWWIDBytes, err := os.ReadFile(filepath.Join("/sys/block", dev.Name(), "device", "wwid"))
		if err != nil {
			continue
		}

		devWWID := normalizeWWID(string(devWWIDBytes))
		if devWWID == "" {
			continue
		}

		// Compare the SCSI device WWID with the multipath WWID.
		//
		// Some multipath WWIDs include the SCSI VPD designator type (3) as a prefix.
		// For example, multipath may report WWID as "368cc...", while the SCSI device
		// reports WWID as "68cc...".
		if devWWID == mpWWID || "3"+devWWID == mpWWID {
			addDeviceIfNotExist(dev.Name())
		}
	}

	return devices, nil
}

// rescanMultipathSCSIDevices triggers a SCSI capacity rescan on the device(s) backing the given
// multipath device.
// For multipath devices, all underlying SCSI devices are rescanned.
// For single-path SCSI devices (sd*), the device itself is rescanned.
func rescanMultipathSCSIDevices(devicePath string) error {
	deviceName := filepath.Base(devicePath)

	var deviceNames []string
	if isMultipathDevice(devicePath) {
		var err error
		deviceNames, err = findMultipathSCSIDevices(deviceName)
		if err != nil {
			return fmt.Errorf("Failed finding slave SCSI devices for %q: %w", devicePath, err)
		}
	} else {
		deviceNames = []string{deviceName}
	}

	for _, name := range deviceNames {
		rescanPath := filepath.Join("/sys/block", name, "device", "rescan")
		err := os.WriteFile(rescanPath, []byte("1"), 0200)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("Failed rescanning SCSI device %q: %w", name, err)
		}
	}

	return nil
}

// waitMultipathReady checks if the multipath device has at least one active path.
func waitMultipathReady(ctx context.Context, devicePath string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		out, err := exec.CommandContext(ctx, "multipath", "-ll", devicePath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("Failed checking multipath device %q status: %w", devicePath, err)
		}

		// The "multipath -ll" outputs the topology of a mapper. We look for a path that is:
		// - running = Kernel SCSI device state is online.
		// - ready   = Path checker confirms the device is usable.
		// - active  = Path group is currently used by multipath.
		if strings.Contains(string(out), "active ready running") {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("Timeout waiting for multipath device %q to have active paths: %w", devicePath, ctx.Err())
		case <-ticker.C:
		}
	}
}
