package warnings

import (
	"fmt"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
)

// ResolveWarningsByLocalNodeAndType resolves warnings with the local node and type code.
// Returns error if no local node name.
func ResolveWarningsByLocalNodeAndType(cluster *db.Cluster, typeCode db.WarningType) error {
	var err error
	var localName string

	err = cluster.Transaction(func(tx *db.ClusterTx) error {
		localName, err = tx.GetLocalNodeName()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return errors.Wrapf(err, "Failed getting local member name")
	}

	if localName == "" {
		return fmt.Errorf("Local member name not available")
	}

	return ResolveWarningsByNodeAndType(cluster, localName, typeCode)
}

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

// ResolveWarningsByNodeAndProjectAndType resolves warnings with the given node, project and type code.
func ResolveWarningsByNodeAndProjectAndType(cluster *db.Cluster, nodeName string, projectName string, typeCode db.WarningType) error {
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		warnings, err := tx.GetWarningsByType(typeCode)
		if err != nil {
			return err
		}

		for _, w := range warnings {
			if w.Node != nodeName || w.Project != projectName {
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

// ResolveWarningsByLocalNodeAndProjectAndType resolves warnings with the given project and type code.
func ResolveWarningsByLocalNodeAndProjectAndType(cluster *db.Cluster, projectName string, typeCode db.WarningType) error {
	var err error
	var localName string

	err = cluster.Transaction(func(tx *db.ClusterTx) error {
		localName, err = tx.GetLocalNodeName()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return errors.Wrap(err, "Failed getting local member name")
	}

	if localName == "" {
		return fmt.Errorf("Local member name not available")
	}

	return ResolveWarningsByNodeAndProjectAndType(cluster, localName, projectName, typeCode)
}
