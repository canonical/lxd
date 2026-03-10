package connectors

import (
	"context"
	"errors"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/revert"
)

// Target represent a connection target.
type Target struct {
	Address       string
	QualifiedName string
}

// groupTargetsAddressesByQualifiedName combines target addresses from targets with the same qualified names together into a single map key.
func groupTargetsByQualifiedName(targets ...Target) map[string][]string {
	grouped := map[string][]string{}
	// Attempt to preserve order while grouping.
	for _, target := range targets {
		grouped[target.QualifiedName] = append(grouped[target.QualifiedName], target.Address)
	}

	for qn, addresses := range grouped {
		grouped[qn] = shared.Unique(addresses)
	}

	return grouped
}

// DiscoverAll is just like Connector.Discover but runs discovery on all targets and combines their logs.
func DiscoverAll(ctx context.Context, c Connector, targetAddresses ...string) ([]any, error) {
	var log []any
	for _, addr := range targetAddresses {
		discovered, err := c.Discover(ctx, addr)
		if err != nil {
			// Underlying connector should log a waring.
			continue
		}

		log = append(log, discovered...)
	}

	if len(log) == 0 {
		return nil, errors.New("Failed to fetch a discovery log record from any of the target addresses")
	}

	return log, nil
}

// ConnectAll is just like Connector.Connect but allows specifying different qualified names for different target addresses.
func ConnectAll(ctx context.Context, c Connector, targets ...Target) (revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	for qn, addresses := range groupTargetsByQualifiedName(targets...) {
		cleanup, err := c.Connect(ctx, qn, addresses...)
		if err != nil {
			return nil, err
		}

		revert.Add(cleanup)
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return cleanup, nil
}

// DisconnectAll is just like Connector.Disconnect but runs disconnect on all targets.
func DisconnectAll(c Connector, targets ...Target) error {
	for qn := range groupTargetsByQualifiedName(targets...) {
		err := c.Disconnect(qn)
		if err != nil {
			return err
		}
	}

	return nil
}
