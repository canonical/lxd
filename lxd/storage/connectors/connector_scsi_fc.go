package connectors

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/revert"
)

var _ Connector = &connectorSCSIFC{}

type connectorSCSIFC struct {
	common
}

// Type returns the type of the connector.
func (c *connectorSCSIFC) Type() string {
	return TypeSCSIFC
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

func (c *connectorSCSIFC) QualifiedName() (string, error) {
	return "", nil
}

func (c *connectorSCSIFC) Connect(ctx context.Context, WWPN string, luns ...string) (revert.Hook, error) {
	return nil, nil
}

// Disconnect is a no-op for FC.
func (c *connectorSCSIFC) Disconnect(targetQN string) error {
	return nil
}

// findSession returns nil as FC doesn't have sessions.
func (c *connectorSCSIFC) findSession(targetQN string) (*session, error) {
	return nil, nil
}

func (c *connectorSCSIFC) Discover(ctx context.Context, targetAddresses ...string) ([]any, error) {
	return nil, nil
}

func (c *connectorSCSIFC) WaitDiskDevicePath(ctx context.Context, diskPathFilter block.DevicePathFilterFunc) (string, error) {
	return "", nil
}

// GetDiskDevicePath returns the path of the mapped SCSI/FC device if it already exists.
func (c *connectorSCSIFC) GetDiskDevicePath(diskPathFilter block.DevicePathFilterFunc) (string, error) {
	return block.GetDiskDevicePath(scsiDiskDevicePrefix, diskPathFilter)
}

func (c *connectorSCSIFC) RemoveDiskDevice(ctx context.Context, devicePath string) error {
	return nil
}

func (c *connectorSCSIFC) WaitDiskDeviceResize(ctx context.Context, devicePath string, newSizeBytes int64) error {
	return block.WaitDiskDeviceResize(ctx, devicePath, newSizeBytes)
}
