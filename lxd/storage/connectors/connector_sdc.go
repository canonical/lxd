package connectors

import (
	"context"
)

var _ Connector = &connectorSDC{}

type connectorSDC struct {
	common
}

// Type returns the type of the connector.
func (c *connectorSDC) Type() string {
	return TypeSDC
}

// LoadModules returns true. SDC does not require any kernel modules to be loaded.
func (c *connectorSDC) LoadModules() bool {
	return true
}

// QualifiedName returns an empty string and no error. SDC has no qualified name.
func (c *connectorSDC) QualifiedName() (string, error) {
	return "", nil
}

// SessionID returns an empty string and no error, as connections are handled by SDC.
func (c *connectorSDC) SessionID(targetQN string) (string, error) {
	return "", nil
}

// Connect does nothing. Connections are fully handled by SDC.
func (c *connectorSDC) Connect(ctx context.Context, targetAddr string, targetQN string) error {
	// Nothing to do. Connection is handled by Dell SDC.
	return nil
}

// ConnectAll does nothing. Connections are fully handled by SDC.
func (c *connectorSDC) ConnectAll(ctx context.Context, targetAddr string) error {
	// Nothing to do. Connection is handled by Dell SDC.
	return nil
}

// Disconnect does nothing. Connections are fully handled by SDC.
func (c *connectorSDC) Disconnect(targetQN string) error {
	return nil
}

// DisconnectAll does nothing. Connections are fully handled by SDC.
func (c *connectorSDC) DisconnectAll() error {
	return nil
}
