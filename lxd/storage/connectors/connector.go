package connectors

import (
	"context"
	"fmt"

	"github.com/canonical/lxd/shared/revert"
)

const (
	// TypeUnknown represents an unknown storage connector.
	TypeUnknown string = "unknown"

	// TypeNVME represents an NVMe/TCP storage connector.
	TypeNVME string = "nvme"

	// TypeSDC represents Dell SDC storage connector.
	TypeSDC string = "sdc"

	// TypeISCSI represents an iSCSI storage connector.
	TypeISCSI string = "iscsi"
)

// session represents a connector session that is established with a target.
type session struct {
	// id is a unique identifier of the session.
	id string

	// targetQN is the qualified name of the target.
	targetQN string

	// addresses is a list of active addresses associated with the session.
	addresses []string
}

// Connector represents a storage connector that handles connections through
// appropriate storage subsystem.
type Connector interface {
	Type() string
	Version() (string, error)
	QualifiedName() (string, error)
	LoadModules() error
	Connect(ctx context.Context, targetQN string, targetAddrs ...string) (revert.Hook, error)
	Disconnect(targetQN string) error
	Discover(ctx context.Context, targetAddresses ...string) ([]any, error)
	findSession(targetQN string) (*session, error)
}

// NewConnector instantiates a new connector of the given type.
// The caller needs to ensure connector type is validated before calling this
// function, as common (empty) connector is returned for unknown type.
func NewConnector(connectorType string, serverUUID string) (Connector, error) {
	common := common{
		serverUUID: serverUUID,
	}

	switch connectorType {
	case TypeNVME:
		return &connectorNVMe{
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
