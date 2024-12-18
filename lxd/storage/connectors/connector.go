package connectors

import (
	"context"
)

const (
	// TypeUnknown represents an unknown storage connector.
	TypeUnknown string = "unknown"
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
