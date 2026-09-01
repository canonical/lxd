package connectors

import (
	"context"
	"fmt"

	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/shared/revert"
)

const (
	// TypeUnknown represents an unknown storage connector.
	TypeUnknown string = "unknown"

	// TypeNVMeTCP represents an NVMe/TCP storage connector.
	TypeNVMeTCP string = "nvme/tcp"

	// TypeNVMeFC represents an NVMe over Fibre Channel storage connector.
	TypeNVMeFC string = "nvme/fc"

	// TypeSDC represents Dell SDC storage connector.
	TypeSDC string = "sdc"

	// TypeISCSI represents an iSCSI storage connector.
	TypeISCSI string = "iscsi"

	// TypeSCSIFC represents a Fibre Channel storage connector.
	TypeSCSIFC string = "scsi/fc"
)

// IsNVMe returns true if the given connector type uses the NVMe protocol,
// regardless of the underlying transport (TCP or Fibre Channel).
func IsNVMe(connectorType string) bool {
	return connectorType == TypeNVMeTCP || connectorType == TypeNVMeFC
}

// TransportType represents the transport type of the storage connector.
type TransportType string

const (
	// TransportTCP represents a TCP-based storage transport.
	TransportTCP TransportType = "tcp"

	// TransportFC represents a Fibre Channel storage transport.
	TransportFC TransportType = "fc"
)

// session represents a connector session that is established with a target.
type session struct {
	// id is a unique identifier of the session.
	id string

	// targetQN is the qualified name of the target.
	targetQN string

	// addresses is a list of active addresses associated with the session.
	addresses []string

	// hostAddressesByTarget maps each target address to the local HBA addresses that
	// already have an established path to it. Populated only for NVMe/FC.
	hostAddressesByTarget map[string][]string
}

// Connector represents a storage connector that handles connections through
// appropriate storage subsystem.
type Connector interface {
	// Type returns the connector's type.
	Type() string

	// Transport returns the connector's transport type.
	Transport() TransportType

	// Version returns a version string identifying the connector's underlying tooling.
	// A non-empty value indicates the connector is usable on this host.
	Version() (string, error)

	// QualifiedName returns the name that uniquely identifies the local host initiator.
	QualifiedName() (string, error)

	// LoadModules loads any required kernel modules for the connector.
	LoadModules() error

	// Connect connects to targetName using protocol-specific target specifiers.
	//
	// Target name identifies the target to connect to.
	// - For iSCSI this is the target IQN.
	// - For NVMe/TCP and NVMe/FC this is the target NQN.
	// - For SCSI/FC this is the target WWPN.
	//
	// Target specifiers are protocol-specific values needed to access the target.
	// - For iSCSI and NVMe/TCP these are target addresses.
	// - For NVMe/FC these are the array's FC target port addresses ("nn-<wwnn>:pn-<wwpn>").
	// - For SCSI/FC these are LUNs.
	Connect(ctx context.Context, targetName string, targetSpecifiers ...string) (revert.Hook, error)

	// Disconnect disconnects from targetName.
	//
	// Target name uses the same protocol-specific identifier as [Connect].
	Disconnect(targetName string) error

	// Discover finds the targets available on the array.
	//
	// Discovery targets are the protocol-specific inputs used to reach the array and
	// enumerate its targets.
	// - For iSCSI these are portal addresses.
	// - For NVMe/TCP these are discovery addresses.
	// - For NVMe/FC these are the array's FC target port addresses ("nn-<wwnn>:pn-<wwpn>").
	// - For SCSI/FC these are target WWPNs.
	Discover(ctx context.Context, discoveryTargets ...string) ([]any, error)

	// GetDiskDevicePath returns the device path of the disk that passes the provided filter function.
	GetDiskDevicePath(diskPathFilter block.DevicePathFilterFunc) (string, error)

	// WaitDiskDevicePath similar to [GetDiskDevicePath] returns the disk device path that
	// passes the provided filter function. If no such device path is found, it waits for it to
	// appear until the context is done.
	WaitDiskDevicePath(ctx context.Context, diskPathFilter block.DevicePathFilterFunc) (string, error)

	// WaitDiskDeviceResize waits for the disk device at the given path to report the new size
	// in bytes.
	WaitDiskDeviceResize(ctx context.Context, diskPath string, newSizeBytes int64) error

	// RemoveDiskDevice ensures the disk device at the given path is removed from the host.
	RemoveDiskDevice(ctx context.Context, devicePath string) error

	// findSession finds an active session for the given target.
	findSession(targetName string) (*session, error)
}

// NewConnector instantiates a new connector of the given type, returning an error for unknown types.
func NewConnector(connectorType string, serverUUID string) (Connector, error) {
	common := common{
		serverUUID: serverUUID,
	}

	switch connectorType {
	case TypeNVMeTCP:
		return &connectorNVMe{
			common: common,
		}, nil

	case TypeNVMeFC:
		return &connectorNVMeFC{
			common: common,
		}, nil

	case TypeSDC:
		return &connectorSDC{
			common: common,
		}, nil

	case TypeISCSI:
		return &connectorISCSI{
			common: common,
		}, nil

	case TypeSCSIFC:
		return &connectorSCSIFC{
			common: common,
		}, nil

	default:
		// Return common connector if the type is unknown. This removes
		// the need to check for nil or handle the error in the caller.
		return nil, fmt.Errorf("Unknown storage connector type %q", connectorType)
	}
}

// GetSupportedVersions returns the versions for the given connector types
// ignoring those that produce an error when version is being retrieved
// (e.g. due to a missing required tools).
func GetSupportedVersions(connectorTypes []string) []string {
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
