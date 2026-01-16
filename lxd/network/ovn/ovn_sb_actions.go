package ovn

import (
	"context"
	"errors"
	"fmt"

	ovnSB "github.com/canonical/lxd/lxd/network/ovn/schema/ovn-sb"
)

// GetLogicalRouterPortActiveChassisHostname gets the hostname of the chassis managing the logical router port.
func (o *SB) GetLogicalRouterPortActiveChassisHostname(ovnRouterPort OVNRouterPort) (string, error) {
	ctx := context.TODO()

	// Look for the port binding.
	pb := &ovnSB.PortBinding{
		LogicalPort: fmt.Sprintf("cr-%s", ovnRouterPort),
	}

	err := o.client.Get(ctx, pb)
	if err != nil {
		return "", err
	}

	if pb.Chassis == nil {
		return "", errors.New("No chassis found")
	}

	// Get the associated chassis.
	chassis := &ovnSB.Chassis{
		UUID: *pb.Chassis,
	}

	err = o.client.Get(ctx, chassis)
	if err != nil {
		return "", err
	}

	return chassis.Hostname, nil
}
