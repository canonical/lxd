//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

import (
	"database/sql"
	"fmt"
	"sort"
	"time"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/shared"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t snapshots.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -p db -e instance_snapshot objects
//go:generate mapper stmt -p db -e instance_snapshot objects-by-Project-and-Instance
//go:generate mapper stmt -p db -e instance_snapshot objects-by-Project-and-Instance-and-Name
//go:generate mapper stmt -p db -e instance_snapshot id
//go:generate mapper stmt -p db -e instance_snapshot create struct=InstanceSnapshot
//go:generate mapper stmt -p db -e instance_snapshot rename
//go:generate mapper stmt -p db -e instance_snapshot delete-by-Project-and-Instance-and-Name
//
//go:generate mapper method -p db -e instance_snapshot GetMany references=Device,Config
//go:generate mapper method -p db -e instance_snapshot GetOne
//go:generate mapper method -p db -e instance_snapshot ID struct=InstanceSnapshot
//go:generate mapper method -p db -e instance_snapshot Exists struct=InstanceSnapshot
//go:generate mapper method -p db -e instance_snapshot Create references=Device,Config
//go:generate mapper method -p db -e instance_snapshot Rename
//go:generate mapper method -p db -e instance_snapshot DeleteOne-by-Project-and-Instance-and-Name

// InstanceSnapshot is a value object holding db-related details about a snapshot.
type InstanceSnapshot struct {
	ID           int
	Project      string `db:"primary=yes&join=projects.name&via=instance"`
	Instance     string `db:"primary=yes&join=instances.name"`
	Name         string `db:"primary=yes"`
	CreationDate time.Time
	Stateful     bool
	Description  string `db:"coalesce=''"`
	ExpiryDate   sql.NullTime
}

// InstanceSnapshotFilter specifies potential query parameter fields.
type InstanceSnapshotFilter struct {
	Project  *string
	Instance *string
	Name     *string
}

// ToInstance is a convenience function to merge
// together an Instance struct and a SnapshotInstance struct into into a the
// legacy Instance struct for a snapshot.
func (s *InstanceSnapshot) ToInstance(instance *Instance) Instance {
	return Instance{
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

// ToInstanceArgs returns an InstanceArgs with the instance information for the snapshot.
func (s *InstanceSnapshot) ToInstanceArgs(tx *ClusterTx, instance *Instance) (*InstanceArgs, error) {
	config, err := tx.GetInstanceSnapshotConfig(s.ID)
	if err != nil {
		return nil, err
	}

	devices, err := tx.GetInstanceSnapshotDevices(s.ID)
	if err != nil {
		return nil, err
	}

	profiles, err := tx.GetInstanceProfiles(*instance)
	if err != nil {
		return nil, err
	}

	profileNames := make([]string, len(profiles))

	for i, p := range profiles {
		profileNames[i] = p.Name
	}

	return &InstanceArgs{
		ID:       s.ID,
		Node:     instance.Node,
		Type:     instance.Type,
		Snapshot: true,

		Project:      s.Project,
		CreationDate: s.CreationDate,

		Architecture: instance.Architecture,
		Config:       config,
		Description:  s.Description,
		Devices:      deviceConfig.NewDevices(DevicesToAPI(devices)),
		Ephemeral:    false,
		LastUsedDate: sql.NullTime{}.Time,
		Name:         instance.Name + shared.SnapshotDelimiter + s.Name,
		Profiles:     profileNames,
		Stateful:     s.Stateful,
		ExpiryDate:   s.ExpiryDate.Time,
	}, nil
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
	defer stmt.Close()

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

// GetInstanceSnapshotID returns the ID of the snapshot with the given name.
func (c *Cluster) GetInstanceSnapshotID(project, instance, name string) (int, error) {
	var id int64
	err := c.Transaction(func(tx *ClusterTx) error {
		var err error
		id, err = tx.GetInstanceSnapshotID(project, instance, name)
		return err
	})
	return int(id), err
}
