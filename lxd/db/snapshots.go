//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/shared"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t snapshots.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -d cluster -p db -e instance_snapshot objects
//go:generate mapper stmt -d cluster -p db -e instance_snapshot objects-by-ID
//go:generate mapper stmt -d cluster -p db -e instance_snapshot objects-by-Project-and-Instance
//go:generate mapper stmt -d cluster -p db -e instance_snapshot objects-by-Project-and-Instance-and-Name
//go:generate mapper stmt -d cluster -p db -e instance_snapshot id
//go:generate mapper stmt -d cluster -p db -e instance_snapshot create struct=InstanceSnapshot
//go:generate mapper stmt -d cluster -p db -e instance_snapshot rename
//go:generate mapper stmt -d cluster -p db -e instance_snapshot delete-by-Project-and-Instance-and-Name
//
//go:generate mapper method -i -d cluster -p db -e instance_snapshot GetMany
//go:generate mapper method -i -d cluster -p db -e instance_snapshot GetOne
//go:generate mapper method -i -d cluster -p db -e instance_snapshot ID struct=InstanceSnapshot
//go:generate mapper method -i -d cluster -p db -e instance_snapshot Exists struct=InstanceSnapshot
//go:generate mapper method -i -d cluster -p db -e instance_snapshot Create struct=InstanceSnapshot
//go:generate mapper method -i -d cluster -p db -e instance_snapshot Rename
//go:generate mapper method -i -d cluster -p db -e instance_snapshot DeleteOne-by-Project-and-Instance-and-Name

// InstanceSnapshot is a value object holding db-related details about a snapshot.
type InstanceSnapshot struct {
	ID           int
	Project      string `db:"primary=yes&join=projects.name&via=instance"`
	Instance     string `db:"primary=yes&join=instances.name"`
	Name         string `db:"primary=yes"`
	CreationDate time.Time
	Stateful     bool
	Description  string `db:"coalesce=''"`
	Config       map[string]string
	Devices      map[string]Device
	ExpiryDate   sql.NullTime
}

// InstanceSnapshotFilter specifies potential query parameter fields.
type InstanceSnapshotFilter struct {
	ID       *int
	Project  *string
	Instance *string
	Name     *string
}

// ToInstance converts an instance snapshot to a database Instance, filling in extra fields from the parent instance.
func (s *InstanceSnapshot) ToInstance(instance *cluster.Instance) cluster.Instance {
	return cluster.Instance{
		ID:           s.ID,
		Project:      s.Project,
		Name:         instance.Name + shared.SnapshotDelimiter + s.Name,
		Node:         instance.Node,
		Type:         instance.Type,
		Snapshot:     true,
		Architecture: instance.Architecture,
		Ephemeral:    false,
		CreationDate: s.CreationDate,
		Stateful:     s.Stateful,
		LastUseDate:  sql.NullTime{},
		Description:  s.Description,
		ExpiryDate:   s.ExpiryDate,
	}
}

// UpdateInstanceSnapshotConfig inserts/updates/deletes the provided config keys.
func (c *ClusterTx) UpdateInstanceSnapshotConfig(id int, values map[string]string) error {
	insertSQL := "INSERT OR REPLACE INTO instances_snapshots_config (instance_snapshot_id, key, value) VALUES"
	deleteSQL := "DELETE FROM instances_snapshots_config WHERE key IN %s AND instance_snapshot_id=?"
	return c.configUpdate(id, values, insertSQL, deleteSQL)
}

// UpdateInstanceSnapshot updates the description and expiry date of the
// instance snapshot with the given ID.
func (c *ClusterTx) UpdateInstanceSnapshot(id int, description string, expiryDate time.Time) error {
	str := fmt.Sprintf("UPDATE instances_snapshots SET description=?, expiry_date=? WHERE id=?")
	stmt, err := c.tx.Prepare(str)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	if expiryDate.IsZero() {
		_, err = stmt.Exec(description, "", id)
	} else {
		_, err = stmt.Exec(description, expiryDate, id)
	}
	if err != nil {
		return err
	}

	return nil
}

// GetLocalExpiredInstanceSnapshots returns a list of expired snapshots.
func (c *ClusterTx) GetLocalExpiredInstanceSnapshots() ([]InstanceSnapshot, error) {
	q := `
	SELECT
		instances_snapshots.id,
		instances_snapshots.expiry_date
	FROM instances_snapshots
	JOIN instances ON instances.id=instances_snapshots.instance_id
	WHERE instances.node_id=? AND instances_snapshots.expiry_date != '0001-01-01T00:00:00Z'
	`

	snapshotIDs := []int{}
	err := c.QueryScan(q, func(scan func(dest ...any) error) error {
		var id int
		var expiry sql.NullTime

		// Read the row.
		err := scan(&id, &expiry)
		if err != nil {
			return err
		}

		// Skip if not expired.
		if !expiry.Valid || expiry.Time.Unix() <= 0 {
			return nil
		}

		if time.Now().Unix()-expiry.Time.Unix() < 0 {
			return nil
		}

		// Add the snapshot.
		snapshotIDs = append(snapshotIDs, id)

		return nil
	}, c.nodeID)
	if err != nil {
		return nil, err
	}

	// Fetch all the expired snapshot details.
	snapshots := make([]InstanceSnapshot, len(snapshotIDs))

	for i, id := range snapshotIDs {
		snap, err := c.GetInstanceSnapshots(InstanceSnapshotFilter{ID: &id})
		if err != nil {
			return nil, err
		}

		snapshots[i] = snap[0]
	}

	return snapshots, nil
}

// GetInstanceSnapshotID returns the ID of the snapshot with the given name.
func (c *Cluster) GetInstanceSnapshotID(project, instance, name string) (int, error) {
	var id int64
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		var err error
		id, err = tx.GetInstanceSnapshotID(project, instance, name)
		return err
	})
	return int(id), err
}
