package warnings

import (
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
)

// ResolveWarningsByNodeAndType resolves warnings with the given node and type code.
func ResolveWarningsByNodeAndType(cluster *db.Cluster, nodeName string, typeCode db.WarningType) error {
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		warnings, err := tx.GetWarningsByType(typeCode)
		if err != nil {
			return err
		}

		for _, w := range warnings {
			if w.Node != nodeName {
				continue
			}

			err = tx.UpdateWarningStatus(w.UUID, db.WarningStatusResolved)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return errors.Wrap(err, "Failed to resolve warnings")
	}

	return nil
}
