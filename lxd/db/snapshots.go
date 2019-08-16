package db

import (
	"time"

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
//go:generate mapper stmt -p db -e instance_snapshot config-ref
//go:generate mapper stmt -p db -e instance_snapshot config-ref-by-Project-and-Instance
//go:generate mapper stmt -p db -e instance_snapshot config-ref-by-Project-and-Instance-and-Name
//go:generate mapper stmt -p db -e instance_snapshot devices-ref
//go:generate mapper stmt -p db -e instance_snapshot devices-ref-by-Project-and-Instance
//go:generate mapper stmt -p db -e instance_snapshot devices-ref-by-Project-and-Instance-and-Name
//go:generate mapper stmt -p db -e instance_snapshot create struct=InstanceSnapshot
//go:generate mapper stmt -p db -e instance_snapshot create-config-ref
//go:generate mapper stmt -p db -e instance_snapshot create-devices-ref
//
//go:generate mapper method -p db -e instance_snapshot List
//go:generate mapper method -p db -e instance_snapshot Get
//go:generate mapper method -p db -e instance_snapshot ID struct=InstanceSnapshot
//go:generate mapper method -p db -e instance_snapshot Exists struct=InstanceSnapshot
//go:generate mapper method -p db -e instance_snapshot Create struct=InstanceSnapshot
//go:generate mapper method -p db -e instance_snapshot ConfigRef
//go:generate mapper method -p db -e instance_snapshot DevicesRef

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
	Devices      map[string]map[string]string
	ExpiryDate   time.Time
}

// InstanceSnapshotFilter can be used to filter results yielded by InstanceSnapshotList.
type InstanceSnapshotFilter struct {
	Project  string
	Instance string
	Name     string
}

// InstanceSnapshotToInstance is a temporary convenience function to merge
// together an Instance struct and a SnapshotInstance struct into into a the
// legacy Instance struct for a snapshot.
func InstanceSnapshotToInstance(instance *Instance, snapshot *InstanceSnapshot) Instance {
	return Instance{
		ID:           snapshot.ID,
		Project:      snapshot.Project,
		Name:         instance.Name + shared.SnapshotDelimiter + snapshot.Name,
		Node:         instance.Node,
		Type:         int(CTypeSnapshot),
		Architecture: instance.Architecture,
		Ephemeral:    false,
		CreationDate: snapshot.CreationDate,
		Stateful:     snapshot.Stateful,
		LastUseDate:  time.Time{},
		Description:  snapshot.Description,
		Config:       snapshot.Config,
		Devices:      snapshot.Devices,
		Profiles:     instance.Profiles,
		ExpiryDate:   snapshot.ExpiryDate,
	}
}
