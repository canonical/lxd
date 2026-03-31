package connectors

import (
	"context"
	"fmt"

	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/shared/revert"
)

// ConnectorType represents connector type.
type ConnectorType string

const (
	// TypeUnknown represents an unknown storage connector.
	TypeUnknown ConnectorType = "unknown"

	// TypeNVME represents an NVMe/TCP storage connector.
	TypeNVME ConnectorType = "nvme"

	// TypeSDC represents Dell SDC storage connector.
	TypeSDC ConnectorType = "sdc"

	// TypeISCSI represents an iSCSI storage connector.
	TypeISCSI ConnectorType = "iscsi"
)

// Connector represents a storage connector that handles connections through
// appropriate storage subsystem.
type Connector interface {
	Type() ConnectorType
	Version() (string, error)
	QualifiedName() (string, error)
	LoadModules() error

	Discover(ctx context.Context, discoveryAddresses ...string) ([]Target, error)
	Connect(ctx context.Context, targets ...Target) (revert.Hook, error)
	Disconnect(ctx context.Context, targets ...Target) error

	GetDiskDevicePath(ctx context.Context, wait bool, diskNameFilter block.DeviceNameFilterFunc) (string, error)
	WaitDiskDeviceResize(ctx context.Context, devicePath string, newSizeBytes int64) error
	RemoveDiskDevice(ctx context.Context, devicePath string) error

	doNotImplement()
}

// NewConnector instantiates a new connector of the given type.
// The caller needs to ensure connector type is validated before calling this
// function, as common (empty) connector is returned for unknown type.
func NewConnector(connectorType ConnectorType, serverUUID string) (Connector, error) {
	switch connectorType {
	case TypeNVME:
		return newConnectorNVMe(serverUUID)

	case TypeSDC:
		return newConnectorSDC(serverUUID)

	case TypeISCSI:
		return newConnectorISCSI(serverUUID)

	default:
		return nil, fmt.Errorf("Unknown storage connector type %q", connectorType)
	}
}

// GetSupportedVersions returns the versions for the given connector types
// ignoring those that produce an error when version is being retrieved
// (e.g. due to a missing required tools).
func GetSupportedVersions(connectorTypes []ConnectorType) []string {
	versions := make([]string, 0, len(connectorTypes))

	// Iterate over the provided connector types, and extract
	// their versions.
	for _, connectorType := range connectorTypes {
		connector, err := NewConnector(connectorType, "")
		if err != nil {
			continue
		}

		version, _ := connector.Version()
		if version != "" {
			versions = append(versions, version)
		}
	}

	return versions
}
