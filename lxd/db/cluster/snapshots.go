package cluster

import (
	"database/sql"
	"time"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t snapshots.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"

//go:generate mapper stmt -e instance_snapshot objects version=2
//go:generate mapper stmt -e instance_snapshot objects-by-Project-and-Instance version=2
//go:generate mapper stmt -e instance_snapshot objects-by-Project-and-Instance-and-Name version=2
//
//go:generate mapper method -i -e instance_snapshot GetMany references=Config,Device version=2

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
