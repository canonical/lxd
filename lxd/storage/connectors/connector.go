package connectors

import (
	"context"
)

const (
	// TypeUnknown represents an unknown storage connector.
	TypeUnknown string = "unknown"

	// TypeISCSI represents an iSCSI storage connector.
	TypeISCSI string = "iscsi"

	// TypeNVME represents an NVMe/TCP storage connector.
	TypeNVME string = "nvme"

	// TypeSDC represents Dell SDC storage connector.
	TypeSDC string = "sdc"
)

// Connector represents a storage connector that handles connections through
// appropriate storage subsystem.
type Connector interface {
	Type() string
	Version() (string, error)
	QualifiedName() (string, error)
	LoadModules() bool
	SessionID(targetQN string) (string, error)
	Connect(ctx context.Context, targetAddr string, targetQN string) error
	ConnectAll(ctx context.Context, targetAddr string) error
	Disconnect(targetQN string) error
	DisconnectAll() error
}

// NewConnector instantiates a new connector of the given type.
// The caller needs to ensure connector type is validated before calling this
// function, as common (empty) connector is returned for unknown type.
func NewConnector(connectorType string, serverUUID string) Connector {
	common := common{
		serverUUID: serverUUID,
	}

	switch connectorType {
	case TypeISCSI:
		return &connectorISCSI{
			common: common,
		}

	case TypeNVME:
		return &connectorNVMe{
			common: common,
		}

	case TypeSDC:
		return &connectorNVMe{
			common: common,
		}

	default:
		// Return common connector if the type is unknown. This removes
		// the need to check for nil or handle the error in the caller.
		return &common
	}
}
