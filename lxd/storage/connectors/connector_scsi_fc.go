package connectors

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/revert"
)

var _ Connector = &connectorSCSIFC{}

type connectorSCSIFC struct {
	common
}

// FCDiscoveryRecord represents an FC target port found on the fabric.
type FCDiscoveryRecord struct {
	PortName string // Target WWPN (for example "2100001b32abcdef").
}

// Type returns the type of the connector.
func (c *connectorSCSIFC) Type() string {
	return TypeSCSIFC
}

// Transport returns the transport type of the connector.
func (c *connectorSCSIFC) Transport() TransportType {
	return TransportFC
}

// Version returns a non-empty string if FC host adapters are present on the system, error otherwise.
func (c *connectorSCSIFC) Version() (string, error) {
	entries, err := os.ReadDir("/sys/class/fc_host")
	if err != nil {
		return "", fmt.Errorf("No FC host adapters found: %w", err)
	}

	if len(entries) == 0 {
		return "", errors.New("No FC host adapters found")
	}

	return "detected (" + TypeSCSIFC + ")", nil
}

// LoadModules loads the FC SCSI transport kernel module.
func (c *connectorSCSIFC) LoadModules() error {
	return util.LoadModule("scsi_transport_fc")
}

// QualifiedName returns the World Wide Port Name (WWPN) of the first FC host initiator.
func (c *connectorSCSIFC) QualifiedName() (string, error) {
	fcHostPath := "/sys/class/fc_host"

	hosts, err := os.ReadDir(fcHostPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("No FC hosts found: Directory %q does not exist", fcHostPath)
		}

		return "", fmt.Errorf("Failed reading FC hosts: %w", err)
	}

	for _, host := range hosts {
		portNameBytes, err := os.ReadFile(filepath.Join(fcHostPath, host.Name(), "port_name"))
		if err != nil {
			continue
		}

		wwpn := normalizeWWPN(string(portNameBytes))
		return wwpn, nil
	}

	return "", errors.New("No FC host initiators found")
}

// Connect triggers a SCSI bus rescan on local hosts that have a remote FC port
// matching WWPN. The HBA driver handles fabric login automatically; the rescan
// makes newly mapped LUNs visible to the host.
func (c *connectorSCSIFC) Connect(ctx context.Context, wwpn string, luns ...string) (revert.Hook, error) {
	rportBasePath := "/sys/class/fc_remote_ports"
	rports, err := os.ReadDir(rportBasePath)
	if err != nil {
		return nil, fmt.Errorf("Failed reading FC remote ports: %w", err)
	}

	if len(luns) == 0 {
		return nil, errors.New("At least one LUN must be provided to connect to an FC target")
	}

	wwpn = normalizeWWPN(wwpn)

	type scanTarget struct {
		host    string
		channel string
		target  string
	}

	var scanTargets []scanTarget
	for _, rport := range rports {
		portNameBytes, err := os.ReadFile(filepath.Join(rportBasePath, rport.Name(), "port_name"))
		if err != nil {
			continue
		}

		portName := normalizeWWPN(string(portNameBytes))
		if portName != wwpn {
			continue
		}

		// rport directory name has form "rport-H:C-R":
		// H = local SCSI host index, C = channel, R = rport index.
		name := strings.TrimPrefix(rport.Name(), "rport-")
		hostIdx, rest, ok := strings.Cut(name, ":")
		if !ok {
			continue
		}

		channel, _, ok := strings.Cut(rest, "-")
		if !ok {
			// Unexpected format, skip
			continue
		}

		targetBytes, err := os.ReadFile(filepath.Join(rportBasePath, rport.Name(), "scsi_target_id"))
		if err != nil {
			// Attribute missing, skip
			continue
		}

		target := strings.TrimSpace(string(targetBytes))

		// If target is -1 or empty, the FC transport class is not bound to a SCSI target yet.
		if target == "-1" || target == "" {
			continue
		}

		scanTarget := scanTarget{
			host:    "host" + hostIdx,
			channel: channel,
			target:  target,
		}

		scanTargets = append(scanTargets, scanTarget)
	}

	if len(scanTargets) == 0 {
		return nil, fmt.Errorf("No FC remote port with WWPN %q found", wwpn)
	}

	// Trigger SCSI bus rescan on each host, by writing the scan parameters to the host's
	// scan file. This will make the newly mapped LUNs visible to the host.
	for _, scanTarget := range scanTargets {
		scanPath := filepath.Join("/sys/class/scsi_host", scanTarget.host, "scan")

		for _, lun := range luns {
			scan := scanTarget.channel + " " + scanTarget.target + " " + lun

			err := os.WriteFile(scanPath, []byte(scan), 0200)
			if err != nil {
				return nil, fmt.Errorf("Failed scanning FC host %q target %q LUN %q: %w", scanTarget.host, scanTarget.target, lun, err)
			}
		}
	}

	cleanup := func() {}
	return cleanup, nil
}

// Disconnect is a no-op for FC.
func (c *connectorSCSIFC) Disconnect(targetQN string) error {
	return nil
}

// findSession returns nil as FC doesn't have sessions.
func (c *connectorSCSIFC) findSession(targetQN string) (*session, error) {
	return nil, nil
}

// Discover returns the FC target ports visible on the fabric.
// If WWPNs are provided they act as an allowlist.
func (c *connectorSCSIFC) Discover(ctx context.Context, wwpns ...string) ([]any, error) {
	rportBasePath := "/sys/class/fc_remote_ports"

	rports, err := os.ReadDir(rportBasePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errors.New("No FC remote ports found")
		}

		return nil, fmt.Errorf("Failed reading FC remote ports: %w", err)
	}

	result := make([]any, 0, len(rports))
	for _, rport := range rports {
		portNameBytes, err := os.ReadFile(filepath.Join(rportBasePath, rport.Name(), "port_name"))
		if err != nil {
			continue
		}

		portName := normalizeWWPN(string(portNameBytes))

		if len(wwpns) > 0 {
			found := false
			for _, wwpn := range wwpns {
				if strings.EqualFold(portName, normalizeWWPN(wwpn)) {
					found = true
					break
				}
			}

			if !found {
				continue
			}
		}

		stateBytes, err := os.ReadFile(filepath.Join(rportBasePath, rport.Name(), "port_state"))
		if err != nil {
			continue
		}

		state := strings.TrimSpace(string(stateBytes))
		if state != "Online" {
			// Skip offline or blocked ports, as they are not usable.
			continue
		}

		record := FCDiscoveryRecord{
			PortName: normalizeWWPN(portName),
		}

		result = append(result, record)
	}

	if len(result) == 0 {
		return nil, errors.New("No SCSI/FC targets found on the fabric")
	}

	return result, nil
}

// WaitDiskDevicePath waits for the mapped FC device to appear.
// If the discovered device is not a multipath device, multipath is forced and the device path
// is looked up again. An error is returned if no multipath device is found after that.
func (c *connectorSCSIFC) WaitDiskDevicePath(ctx context.Context, diskPathFilter block.DevicePathFilterFunc) (string, error) {
	_, ok := ctx.Deadline()
	if !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	devicePath, err := block.WaitDiskDevicePath(ctx, scsiDiskDevicePrefix, diskPathFilter)
	if err != nil {
		return "", err
	}

	if isMultipathDevice(devicePath) {
		err = waitMultipathReady(ctx, devicePath)
		if err != nil {
			return "", err
		}

		return devicePath, nil
	}

	// Device is not a multipath device.
	// Create multipath device from a found device path.
	_, err = shared.RunCommand(ctx, "multipath", devicePath)
	if err != nil {
		return "", fmt.Errorf("Failed configuring multipath for SCSI/FC device %q: %w", devicePath, err)
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

// GetDiskDevicePath returns the path of the mapped SCSI/FC device if it already exists.
func (c *connectorSCSIFC) GetDiskDevicePath(diskPathFilter block.DevicePathFilterFunc) (string, error) {
	return block.GetDiskDevicePath(scsiDiskDevicePrefix, diskPathFilter)
}

// RemoveDiskDevice removes the FC disk device from the system.
//
// The devices should be removed from the host before being unmapped on the storage array.
// Removing a LUN mapping immediately can cause the device to be trapped in unresponsive (D state)
// if there are still open references to it, for example by udev.
func (c *connectorSCSIFC) RemoveDiskDevice(ctx context.Context, devicePath string) error {
	if devicePath == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	deviceName := filepath.Base(devicePath)

	// removeDevice removes device from the system if the device is removable.
	removeDevice := func(devName string) error {
		path := filepath.Join("/sys/block", devName, "device", "delete")

		err := os.WriteFile(path, []byte("1"), 0200)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}

		return nil
	}

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

		var flushErr error
		for range 10 {
			_, flushErr = shared.RunCommand(ctx, "multipath", "-f", devicePath)

			// Break if successful or if the device vanished during the command.
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

		// Remove underlying SCSI devices.
		for _, devName := range slaveDevices {
			err := removeDevice(devName)
			if err != nil {
				return fmt.Errorf("Failed removing SCSI path %q for %q: %w", devName, devicePath, err)
			}
		}

		// Wait for each SCSI devices to actually disappear.
		for _, devName := range slaveDevices {
			slavePath := filepath.Join("/sys/block", devName)
			if !block.WaitDiskDeviceGone(ctx, slavePath) {
				return fmt.Errorf("Timeout exceeded waiting for SCSI path %q of %q to disappear", devName, devicePath)
			}
		}
	} else {
		// For non-multipath device (/dev/sd*), remove the device itself.
		err := removeDevice(deviceName)
		if err != nil {
			return fmt.Errorf("Failed removing device %q: %w", devicePath, err)
		}

		if !block.WaitDiskDeviceGone(ctx, devicePath) {
			return fmt.Errorf("Timeout exceeded waiting for SCSI device %q to disappear", devicePath)
		}
	}

	return nil
}

// WaitDiskDeviceResize waits until the SCSI/FC disk device reflects the new size.
// For multipath devices the device-mapper map is refreshed before waiting.
func (c *connectorSCSIFC) WaitDiskDeviceResize(ctx context.Context, devicePath string, newSizeBytes int64) error {
	_, ok := ctx.Deadline()
	if !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	// Trigger a SCSI capacity rescan so the kernel reports the new size.
	err := rescanMultipathSCSIDevices(devicePath)
	if err != nil {
		return err
	}

	if isMultipathDevice(devicePath) {
		_, err := shared.RunCommand(ctx, "multipath", "-r", devicePath)
		if err != nil {
			return fmt.Errorf("Failed updating multipath SCSI/FC device %q size: %w", devicePath, err)
		}
	}

	return block.WaitDiskDeviceResize(ctx, devicePath, newSizeBytes)
}

// normalizeWWPN normalizes the WWPN string to make it comparable regardless of the format
// it's provided in. Linux sysfs reports WWPNs as "0x" with 16 hex chars ("0x210034800d7035b3"),
// while storage array might report it using colon-separated byte format ("21:00:34:80:0d:70:35:b3").
func normalizeWWPN(wwpn string) string {
	wwpn = strings.TrimSpace(wwpn)
	wwpn = strings.ToLower(wwpn)
	wwpn = strings.TrimPrefix(wwpn, "0x")
	wwpn = strings.ReplaceAll(wwpn, ":", "")
	return wwpn
}
