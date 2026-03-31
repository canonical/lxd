package connectors

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/shared/revert"
)

type connectorSDC struct {
	common
}

func newConnectorSDC(serverUUID string) (Connector, error) {
	c := &connectorSDC{
		common: common{
			serverUUID: serverUUID,
		},
	}

	return c, nil
}

// SDCDevicePath represents the SDC device once the respective kernel module is loaded.
const SDCDevicePath = "/dev/scini"

// sdcDiskDevicePrefix is the prefix of the SDC disk device name in /dev/disk/by-id/.
const sdcDiskDevicePrefix = "emc-vol-"

// Type returns the type of the connector.
func (c *connectorSDC) Type() ConnectorType {
	return TypeSDC
}

// Version returns an empty string and no error.
func (c *connectorSDC) Version() (string, error) {
	return "", nil
}

// drvCfgIsSDCInstalled checks if the SDC kernel module is loaded.
func (c *connectorSDC) drvCfgIsSDCInstalled() bool {
	// Check to see if the SDC device is available.
	info, err := os.Stat(SDCDevicePath)
	if err != nil {
		return false
	}

	return !info.IsDir()
}

// LoadModules checks if the respective SDC kernel module got already loaded outside of LXD.
// It doesn't try to load the module as LXD doesn't have any control over it.
func (c *connectorSDC) LoadModules() error {
	ok := c.drvCfgIsSDCInstalled()
	if !ok {
		return errors.New("SDC kernel module is not loaded")
	}

	return nil
}

// QualifiedName returns an empty string and no error. SDC has no qualified name.
func (c *connectorSDC) QualifiedName() (string, error) {
	return "", nil
}

// Discover returns the targets found on the first reachable targetAddr.
func (c *connectorSDC) Discover(ctx context.Context, discoveryAddresses ...string) ([]Target, error) {
	return nil, ErrNotSupported
}

// Connect does nothing. Connections are fully handled by SDC.
func (c *connectorSDC) Connect(ctx context.Context, targets ...Target) (revert.Hook, error) {
	// Nothing to do. Connection is handled by Dell SDC.
	return revert.New().Fail, nil
}

// Disconnect does nothing. Connections are fully handled by SDC.
func (c *connectorSDC) Disconnect(ctx context.Context, targets ...Target) error {
	return nil
}

// GetDiskDevicePath returns the path of the mapped SDC device. If the wait
// parameter is true additionally waits for the mapped device to appear and
// returns its path.
func (c *connectorSDC) GetDiskDevicePath(ctx context.Context, wait bool, diskNameFilter block.DeviceNameFilterFunc) (string, error) {
	if diskNameFilter == nil {
		diskNameFilter = func(diskPath string) bool { return true }
	}

	return c.common.GetDiskDevicePath(ctx, wait, func(diskPath string) bool {
		return strings.HasPrefix(diskPath, sdcDiskDevicePrefix) && diskNameFilter(diskPath)
	})
}
