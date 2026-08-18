package connectors

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	ports, err := fcHostPorts()
	if err != nil {
		return "", err
	}

	if len(ports) == 0 {
		return "", errors.New("No FC host adapters found")
	}

	return "detected (" + TypeSCSIFC + ")", nil
}

// LoadModules loads the FC SCSI transport kernel module.
func (c *connectorSCSIFC) LoadModules() error {
	return util.LoadModule("scsi_transport_fc")
}

// QualifiedName returns the World Wide Port Name (WWPN) of the first FC host initiator.
//
// A Fibre Channel host has one WWPN per host bus adapter port, so this reports only
// part of the host's identity. Callers that register the host with a storage array
// should use the package-level [QualifiedNames] helper instead, so that every port is
// registered and every path is usable. That helper takes a [Connector], so it is
// reachable from other packages, which the method of the same name on this unexported
// type is not.
func (c *connectorSCSIFC) QualifiedName() (string, error) {
	wwpns, err := c.QualifiedNames()
	if err != nil {
		return "", err
	}

	return wwpns[0], nil
}

// QualifiedNames returns the World Wide Port Names (WWPNs) of every FC host initiator,
// sorted so that the result is stable across reboots and HBA re-enumeration.
//
// Unlike iSCSI and NVMe, where a host has a single qualified name, a Fibre Channel
// host is identified by the WWPN of each of its host bus adapter ports. Registering
// only one of them leaves the remaining ports unable to see the array's volumes.
func (c *connectorSCSIFC) QualifiedNames() ([]string, error) {
	ports, err := fcHostPorts()
	if err != nil {
		return nil, err
	}

	if len(ports) == 0 {
		return nil, errors.New("No FC host initiators found")
	}

	// Extract unique WWPNs from the list of FC host ports.
	wwpns := make([]string, 0, len(ports))
	for _, port := range ports {
		wwpn := block.NormalizeWWN(port.portName)
		if !slices.Contains(wwpns, wwpn) {
			wwpns = append(wwpns, wwpn)
		}
	}

	return wwpns, nil
}

// Connect triggers a SCSI bus rescan on local hosts that have a remote FC port
// matching WWPN. The HBA driver handles fabric login automatically; the rescan
// makes newly mapped LUNs visible to the host.
func (c *connectorSCSIFC) Connect(ctx context.Context, wwpn string, luns ...string) (revert.Hook, error) {
	if len(luns) == 0 {
		return nil, errors.New("At least one LUN must be provided to connect to an FC target")
	}

	remotePorts, err := fcRemotePorts()
	if err != nil {
		return nil, err
	}

	wwpn = block.NormalizeWWN(wwpn)

	type scanTarget struct {
		host    string
		channel string
		target  string
	}

	var scanTargets []scanTarget
	for _, port := range remotePorts {
		if block.NormalizeWWN(port.portName) != wwpn {
			continue
		}

		// If the SCSI target id is -1 or empty, the FC transport class is not bound
		// to a SCSI target yet.
		if port.scsiTargetID == "-1" || port.scsiTargetID == "" {
			continue
		}

		scanTarget := scanTarget{
			host:    port.host,
			channel: port.channel,
			target:  port.scsiTargetID,
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

// Discover returns the FC target ports that are visible on the fabric and whose
// port name matches one of the allowed WWPNs.
func (c *connectorSCSIFC) Discover(ctx context.Context, wwpns ...string) ([]any, error) {
	if len(wwpns) == 0 {
		return nil, errors.New("No FC target WWPNs provided")
	}

	remotePorts, err := fcRemotePorts()
	if err != nil {
		return nil, err
	}

	result := make([]any, 0, len(remotePorts))
	for _, port := range remotePorts {
		portName := block.NormalizeWWN(port.portName)

		portFound := slices.ContainsFunc(wwpns, func(wwpn string) bool {
			return portName == block.NormalizeWWN(wwpn)
		})

		if !portFound {
			// Skip ports that are not in the list of WWPNs that should be scanned.
			continue
		}

		if port.portState != "Online" {
			// Skip offline or blocked ports, as they are not usable.
			continue
		}

		record := FCDiscoveryRecord{
			PortName: portName,
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
