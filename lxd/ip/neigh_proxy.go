package ip

import (
	"context"
	"net"

	"github.com/canonical/lxd/shared"
)

// NeighProxy represents arguments for neighbour proxy manipulation.
type NeighProxy struct {
	DevName string
	Addr    net.IP
}

// Add a neighbour proxy entry.
func (n *NeighProxy) Add() error {
	_, err := shared.RunCommandContext(context.TODO(), "ip", "neigh", "add", "proxy", n.Addr.String(), "dev", n.DevName)
	if err != nil {
		return err
	}

	return nil
}

// Delete a neighbour proxy entry.
func (n *NeighProxy) Delete() error {
	_, err := shared.RunCommandContext(context.TODO(), "ip", "neigh", "delete", "proxy", n.Addr.String(), "dev", n.DevName)
	if err != nil {
		return err
	}

	return nil
}
