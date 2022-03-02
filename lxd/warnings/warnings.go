package warnings

import (
	"fmt"
	"time"

	"github.com/lxc/lxd/lxd/db"
)

// ResolveWarningsByLocalNodeOlderThan resolves all warnings which are older than the provided time.
func ResolveWarningsByLocalNodeOlderThan(cluster *db.Cluster, date time.Time) error {
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
		return fmt.Errorf("Failed getting local member name: %w", err)
	}

	if localName == "" {
		return fmt.Errorf("Local member name not available")
	}

	err = cluster.Transaction(func(tx *db.ClusterTx) error {
		warnings, err := tx.GetWarnings(db.WarningFilter{})
		if err != nil {
			return err
		}

		for _, w := range warnings {
			if w.Node != localName {
				continue
			}

			if w.LastSeenDate.Before(date) {
				err = tx.UpdateWarningStatus(w.UUID, db.WarningStatusResolved)
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to resolve warnings: %w", err)
	}

	return nil
}

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
		return fmt.Errorf("Failed getting local member name: %w", err)
	}

	if localName == "" {
		return fmt.Errorf("Local member name not available")
	}

	return ResolveWarningsByNodeAndType(cluster, localName, typeCode)
}

// ResolveWarningsByNodeAndType resolves warnings with the given node and type code.
func ResolveWarningsByNodeAndType(cluster *db.Cluster, nodeName string, typeCode db.WarningType) error {
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		filter := db.WarningFilter{
			TypeCode: &typeCode,
			Node:     &nodeName,
		}

		warnings, err := tx.GetWarnings(filter)
		if err != nil {
			return err
		}

		for _, w := range warnings {
			err = tx.UpdateWarningStatus(w.UUID, db.WarningStatusResolved)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to resolve warnings: %w", err)
	}

	return nil
}

// ResolveWarningsByNodeAndProjectAndType resolves warnings with the given node, project and type code.
func ResolveWarningsByNodeAndProjectAndType(cluster *db.Cluster, nodeName string, projectName string, typeCode db.WarningType) error {
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		filter := db.WarningFilter{
			TypeCode: &typeCode,
			Node:     &nodeName,
			Project:  &projectName,
		}

		warnings, err := tx.GetWarnings(filter)
		if err != nil {
			return err
		}

		for _, w := range warnings {
			err = tx.UpdateWarningStatus(w.UUID, db.WarningStatusResolved)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to resolve warnings: %w", err)
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
		return fmt.Errorf("Failed getting local member name: %w", err)
	}

	if localName == "" {
		return fmt.Errorf("Local member name not available")
	}

	return ResolveWarningsByNodeAndProjectAndType(cluster, localName, projectName, typeCode)
}

// ResolveWarningsByNodeAndProjectAndTypeAndEntity resolves warnings with the given node, project, type code, and entity.
func ResolveWarningsByNodeAndProjectAndTypeAndEntity(cluster *db.Cluster, nodeName string, projectName string, typeCode db.WarningType, entityTypeCode int, entityID int) error {
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		filter := db.WarningFilter{
			TypeCode:       &typeCode,
			Node:           &nodeName,
			Project:        &projectName,
			EntityTypeCode: &entityTypeCode,
			EntityID:       &entityID,
		}

		warnings, err := tx.GetWarnings(filter)
		if err != nil {
			return err
		}

		for _, w := range warnings {
			err = tx.UpdateWarningStatus(w.UUID, db.WarningStatusResolved)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to resolve warnings: %w", err)
	}

	return nil
}

// ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity resolves warnings with the given project, type code, and entity.
func ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(cluster *db.Cluster, projectName string, typeCode db.WarningType, entityTypeCode int, entityID int) error {
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
		return fmt.Errorf("Failed getting local member name: %w", err)
	}

	if localName == "" {
		return fmt.Errorf("Local member name not available")
	}

	return ResolveWarningsByNodeAndProjectAndTypeAndEntity(cluster, localName, projectName, typeCode, entityTypeCode, entityID)
}

// DeleteWarningsByNodeAndProjectAndTypeAndEntity deletes warnings with the given node, project, type code, and entity.
func DeleteWarningsByNodeAndProjectAndTypeAndEntity(cluster *db.Cluster, nodeName string, projectName string, typeCode db.WarningType, entityTypeCode int, entityID int) error {
	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		filter := db.WarningFilter{
			TypeCode:       &typeCode,
			Node:           &nodeName,
			Project:        &projectName,
			EntityTypeCode: &entityTypeCode,
			EntityID:       &entityID,
		}

		warnings, err := tx.GetWarnings(filter)
		if err != nil {
			return err
		}

		for _, w := range warnings {
			err = tx.DeleteWarning(w.UUID)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to delete warnings: %w", err)
	}

	return nil
}

// DeleteWarningsByLocalNodeAndProjectAndTypeAndEntity resolves warnings with the given project, type code, and entity.
func DeleteWarningsByLocalNodeAndProjectAndTypeAndEntity(cluster *db.Cluster, projectName string, typeCode db.WarningType, entityTypeCode int, entityID int) error {
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
		return fmt.Errorf("Failed getting local member name: %w", err)
	}

	if localName == "" {
		return fmt.Errorf("Local member name not available")
	}

	return DeleteWarningsByNodeAndProjectAndTypeAndEntity(cluster, localName, projectName, typeCode, entityTypeCode, entityID)
}
