//go:build linux && cgo && !agent

package cluster

import (
	"database/sql"
	"time"

	"github.com/lxc/lxd/shared"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t snapshots.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"

//go:generate mapper stmt -e instance_snapshot objects version=2
//go:generate mapper stmt -e instance_snapshot objects-by-ID version=2
//go:generate mapper stmt -e instance_snapshot objects-by-Project-and-Instance version=2
//go:generate mapper stmt -e instance_snapshot objects-by-Project-and-Instance-and-Name version=2
//go:generate mapper stmt -e instance_snapshot id version=2
//go:generate mapper stmt -e instance_snapshot create references=Config,Devices version=2
//go:generate mapper stmt -e instance_snapshot rename version=2
//go:generate mapper stmt -e instance_snapshot delete-by-Project-and-Instance-and-Name version=2
//
//go:generate mapper method -i -e instance_snapshot GetMany references=Config,Device version=2
//go:generate mapper method -i -e instance_snapshot GetOne version=2
//go:generate mapper method -i -e instance_snapshot ID version=2
//go:generate mapper method -i -e instance_snapshot Exists version=2
//go:generate mapper method -i -e instance_snapshot Create references=Config,Device version=2
//go:generate mapper method -i -e instance_snapshot Rename version=2
//go:generate mapper method -i -e instance_snapshot DeleteOne-by-Project-and-Instance-and-Name version=2

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
	ID       *int
	Project  *string
	Instance *string
	Name     *string
}

// ToInstance converts an instance snapshot to a database Instance, filling in extra fields from the parent instance.
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
