package cluster

import (
	"fmt"

	"github.com/lxc/lxd/lxd/db"
)

// ResolveTarget is a convenience for handling the value ?targetNode query
// parameter. It returns the address of the given node, or the empty string if
// the given node is the local one.
func ResolveTarget(cluster *db.Cluster, target string) (string, error) {
	address := ""
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		name, err := tx.NodeName()
		if err != nil {
			return err
		}

		if target == name {
			return nil
		}

		node, err := tx.NodeByName(target)
		if err != nil {
			if err == db.ErrNoSuchObject {
				return fmt.Errorf("No cluster member called '%s'", target)
			}

			return err
		}

		if node.Name != name {
			address = node.Address
		}

		return nil
	})

	return address, err
}
