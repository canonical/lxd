package connectors

import (
	"context"
	"errors"

	"github.com/dell/goscaleio"

	"github.com/canonical/lxd/shared/revert"
)

var _ Connector = &connectorSDC{}

type connectorSDC struct {
	common
}

// Type returns the type of the connector.
func (c *connectorSDC) Type() string {
	return TypeSDC
}

// Version returns an empty string and no error.
func (c *connectorSDC) Version() (string, error) {
	return "", nil
}

// LoadModules checks if the respective SDC kernel module got already loaded outside of LXD.
// It doesn't try to load the module as LXD doesn't have any control over it.
func (c *connectorSDC) LoadModules() error {
	ok := goscaleio.DrvCfgIsSDCInstalled()
	if !ok {
		return errors.New("SDC kernel module is not loaded")
	}

	return nil
}

// QualifiedName returns an empty string and no error. SDC has no qualified name.
func (c *connectorSDC) QualifiedName() (string, error) {
	return "", nil
}

// Connect does nothing. Connections are fully handled by SDC.
func (c *connectorSDC) Connect(ctx context.Context, targetQN string, targetAddresses ...string) (revert.Hook, error) {
	// Nothing to do. Connection is handled by Dell SDC.
	return revert.New().Fail, nil
}

// Disconnect does nothing. Connections are fully handled by SDC.
func (c *connectorSDC) Disconnect(targetQN string) error {
	return nil
}

func (c *connectorSDC) findSession(targetQN string) (*session, error) {
	return nil, nil
}

// Discover returns the targets found on the first reachable targetAddr.
func (c *connectorSDC) Discover(ctx context.Context, targetAddresses ...string) ([]any, error) {
	return nil, ErrNotSupported
}
