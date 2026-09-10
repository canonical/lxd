//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/canonical/lxd/lxd/instance/instancetype"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t instances.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e instance objects
//go:generate mapper stmt -e instance objects-by-ID
//go:generate mapper stmt -e instance objects-by-Project
//go:generate mapper stmt -e instance objects-by-Project-and-Type
//go:generate mapper stmt -e instance objects-by-Project-and-Type-and-Node
//go:generate mapper stmt -e instance objects-by-Project-and-Type-and-Node-and-Name
//go:generate mapper stmt -e instance objects-by-Project-and-Type-and-Name
//go:generate mapper stmt -e instance objects-by-Project-and-Name
//go:generate mapper stmt -e instance objects-by-Project-and-Name-and-Node
//go:generate mapper stmt -e instance objects-by-Project-and-Node
//go:generate mapper stmt -e instance objects-by-Type
//go:generate mapper stmt -e instance objects-by-Type-and-Name
//go:generate mapper stmt -e instance objects-by-Type-and-Name-and-Node
//go:generate mapper stmt -e instance objects-by-Type-and-Node
//go:generate mapper stmt -e instance objects-by-Node
//go:generate mapper stmt -e instance objects-by-Node-and-Name
//go:generate mapper stmt -e instance objects-by-Name
//go:generate mapper stmt -e instance id
//go:generate mapper stmt -e instance create
//go:generate mapper stmt -e instance rename
//go:generate mapper stmt -e instance delete-by-Project-and-Name
//go:generate mapper stmt -e instance update
//
//go:generate mapper method -i -e instance GetMany references=Config,Device
//go:generate mapper method -i -e instance GetOne
//go:generate mapper method -i -e instance ID
//go:generate mapper method -i -e instance Create references=Config,Device
//go:generate mapper method -i -e instance Rename
//go:generate mapper method -i -e instance DeleteOne-by-Project-and-Name
//go:generate mapper method -i -e instance Update references=Config,Device
//go:generate goimports -w instances.mapper.go
//go:generate goimports -w instances.interface.mapper.go

// Instance is a value object holding db-related details about an instance.
type Instance struct {
	ID           int
	Project      string `db:"primary=yes&join=projects.name"`
	Name         string `db:"primary=yes"`
	Node         string `db:"join=nodes.name"`
	Type         instancetype.Type
	Snapshot     bool `db:"ignore"`
	Architecture int
	Ephemeral    bool
	CreationDate time.Time
	Stateful     bool
	LastUseDate  sql.NullTime
	Description  string `db:"coalesce=''"`
	ExpiryDate   sql.NullTime
}

// InstanceFilter specifies potential query parameter fields.
type InstanceFilter struct {
	ID      *int
	Project *string
	Name    *string
	Node    *string
	Type    *instancetype.Type
}

// GetMostRecentSnapshotCreationDate returns the creation date of the most recent snapshot of the instance with the given ID.
// It returns nil if there are no snapshots.
func GetMostRecentSnapshotCreationDate(ctx context.Context, tx *sql.Tx, instanceID int64) (*time.Time, error) {
	var creationDate time.Time
	row := tx.QueryRowContext(ctx, `SELECT creation_date FROM instances_snapshots WHERE instance_id = ? ORDER BY creation_date DESC LIMIT 1`, instanceID)
	err := row.Scan(&creationDate)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("Failed getting last snapshot creation date: %w", err)
		}

		return nil, nil
	}

	return &creationDate, nil
}
