//go:build linux && cgo && !agent

package db

import (
	"context"
	"fmt"
	"time"

	"github.com/pborman/uuid"

	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t warnings.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -d cluster -p db -e warning objects
//go:generate mapper stmt -d cluster -p db -e warning objects-by-UUID
//go:generate mapper stmt -d cluster -p db -e warning objects-by-Project
//go:generate mapper stmt -d cluster -p db -e warning objects-by-Status
//go:generate mapper stmt -d cluster -p db -e warning objects-by-Node-and-TypeCode
//go:generate mapper stmt -d cluster -p db -e warning objects-by-Node-and-TypeCode-and-Project
//go:generate mapper stmt -d cluster -p db -e warning objects-by-Node-and-TypeCode-and-Project-and-EntityTypeCode-and-EntityID
//go:generate mapper stmt -d cluster -p db -e warning delete-by-UUID
//go:generate mapper stmt -d cluster -p db -e warning delete-by-EntityTypeCode-and-EntityID
//go:generate mapper stmt -d cluster -p db -e warning id
//
//go:generate mapper method -i -d cluster -p db -e warning GetMany
//go:generate mapper method -i -d cluster -p db -e warning GetOne-by-UUID
//go:generate mapper method -i -d cluster -p db -e warning DeleteOne-by-UUID
//go:generate mapper method -i -d cluster -p db -e warning DeleteMany-by-EntityTypeCode-and-EntityID
//go:generate mapper method -i -d cluster -p db -e warning ID struct=Warning
//go:generate mapper method -i -d cluster -p db -e warning Exists struct=Warning

// Warning is a value object holding db-related details about a warning.
type Warning struct {
	ID             int
	Node           string `db:"coalesce=''&leftjoin=nodes.name"`
	Project        string `db:"coalesce=''&leftjoin=projects.name"`
	EntityTypeCode int    `db:"coalesce=-1"`
	EntityID       int    `db:"coalesce=-1"`
	UUID           string `db:"primary=yes"`
	TypeCode       WarningType
	Status         WarningStatus
	FirstSeenDate  time.Time
	LastSeenDate   time.Time
	UpdatedDate    time.Time
	LastMessage    string
	Count          int
}

// WarningFilter specifies potential query parameter fields.
type WarningFilter struct {
	ID             *int
	UUID           *string
	Project        *string
	Node           *string
	TypeCode       *WarningType
	EntityTypeCode *int
	EntityID       *int
	Status         *WarningStatus
}

var warningCreate = cluster.RegisterStmt(`
INSERT INTO warnings (node_id, project_id, entity_type_code, entity_id, uuid, type_code, status, first_seen_date, last_seen_date, updated_date, last_message, count)
  VALUES ((SELECT nodes.id FROM nodes WHERE nodes.name = ?), (SELECT projects.id FROM projects WHERE projects.name = ?), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`)

// UpsertWarningLocalNode creates or updates a warning for the local member. Returns error if no local member name.
func (c *Cluster) UpsertWarningLocalNode(projectName string, entityTypeCode int, entityID int, typeCode WarningType, message string) error {
	var err error
	var localName string

	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
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

	return c.UpsertWarning(localName, projectName, entityTypeCode, entityID, typeCode, message)

}

// UpsertWarning creates or updates a warning.
func (c *Cluster) UpsertWarning(nodeName string, projectName string, entityTypeCode int, entityID int, typeCode WarningType, message string) error {
	// Validate
	_, err := c.GetURIFromEntity(entityTypeCode, entityID)
	if err != nil {
		return fmt.Errorf("Failed to get URI for entity ID %d with entity type code %d: %w", entityID, entityTypeCode, err)
	}

	_, ok := WarningTypeNames[typeCode]
	if !ok {
		return fmt.Errorf("Unknown warning type code %d", typeCode)
	}

	now := time.Now()

	err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		filter := WarningFilter{
			TypeCode:       &typeCode,
			Node:           &nodeName,
			Project:        &projectName,
			EntityTypeCode: &entityTypeCode,
			EntityID:       &entityID,
		}

		warnings, err := tx.GetWarnings(filter)
		if err != nil {
			return fmt.Errorf("Failed to retrieve warnings: %w", err)
		}

		if len(warnings) > 1 {
			// This shouldn't happen
			return fmt.Errorf("More than one warnings (%d) match the criteria: typeCode: %d, nodeName: %q, projectName: %q, entityTypeCode: %d, entityID: %d", len(warnings), typeCode, nodeName, projectName, entityTypeCode, entityID)
		} else if len(warnings) == 1 {
			// If there is a historical warning that was previously automatically resolved and the same
			// warning has now reoccurred then set the status back to WarningStatusNew so it shows as
			// a current active warning.
			newStatus := warnings[0].Status
			if newStatus == WarningStatusResolved {
				newStatus = WarningStatusNew
			}

			err = tx.UpdateWarningState(warnings[0].UUID, message, newStatus)
		} else {
			warning := Warning{
				Node:           nodeName,
				Project:        projectName,
				EntityTypeCode: entityTypeCode,
				EntityID:       entityID,
				UUID:           uuid.New(),
				TypeCode:       typeCode,
				Status:         WarningStatusNew,
				FirstSeenDate:  now,
				LastSeenDate:   now,
				UpdatedDate:    time.Time{},
				LastMessage:    message,
				Count:          1,
			}

			_, err = tx.createWarning(warning)
		}
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// UpdateWarningStatus updates the status of the warning with the given UUID.
func (c *ClusterTx) UpdateWarningStatus(UUID string, status WarningStatus) error {
	str := "UPDATE warnings SET status=?, updated_date=? WHERE uuid=?"
	stmt, err := c.tx.Prepare(str)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	_, err = stmt.Exec(status, time.Now(), UUID)
	if err != nil {
		return fmt.Errorf("Failed to update warning status for warning %q: %w", UUID, err)
	}

	return nil
}

// UpdateWarningState updates the warning message and status with the given ID.
func (c *ClusterTx) UpdateWarningState(UUID string, message string, status WarningStatus) error {
	str := "UPDATE warnings SET last_message=?, last_seen_date=?, updated_date=?, status = ?, count=count+1 WHERE uuid=?"
	stmt, err := c.tx.Prepare(str)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	now := time.Now()

	_, err = stmt.Exec(message, now, now, status, UUID)
	if err != nil {
		return fmt.Errorf("Failed to update warning %q: %w", UUID, err)
	}

	return nil
}

// createWarning adds a new warning to the database.
func (c *ClusterTx) createWarning(object Warning) (int64, error) {
	// Check if a warning with the same key exists.
	exists, err := c.WarningExists(object.UUID)
	if err != nil {
		return -1, fmt.Errorf("Failed to check for duplicates: %w", err)
	}
	if exists {
		return -1, fmt.Errorf("This warning already exists")
	}

	args := make([]any, 12)

	// Populate the statement arguments.
	if object.Node != "" {
		// Ensure node exists
		_, err = c.GetNodeByName(object.Node)
		if err != nil {
			return -1, fmt.Errorf("Failed to get node: %w", err)
		}

		args[0] = object.Node
	}

	if object.Project != "" {
		// Ensure project exists
		projects, err := cluster.GetProjectNames(context.Background(), c.tx)
		if err != nil {
			return -1, fmt.Errorf("Failed to get project names: %w", err)
		}

		if !shared.StringInSlice(object.Project, projects) {
			return -1, fmt.Errorf("Unknown project %q", object.Project)
		}

		args[1] = object.Project
	}

	if object.EntityTypeCode != -1 {
		args[2] = object.EntityTypeCode
	}

	if object.EntityID != -1 {
		args[3] = object.EntityID
	}

	args[4] = object.UUID
	args[5] = object.TypeCode
	args[6] = object.Status
	args[7] = object.FirstSeenDate
	args[8] = object.LastSeenDate
	args[9] = object.UpdatedDate
	args[10] = object.LastMessage
	args[11] = object.Count

	// Prepared statement to use.
	stmt := c.stmt(warningCreate)

	// Execute the statement.
	result, err := stmt.Exec(args...)
	if err != nil {
		return -1, fmt.Errorf("Failed to create warning: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return -1, fmt.Errorf("Failed to fetch warning ID: %w", err)
	}

	return id, nil
}

// ToAPI returns a LXD API entry.
func (w Warning) ToAPI(c *Cluster) (api.Warning, error) {
	typeCode := WarningType(w.TypeCode)

	entity, err := c.GetURIFromEntity(w.EntityTypeCode, w.EntityID)
	if err != nil {
		logger.Warn("Failed to get entity URI for warning", logger.Ctx{"ID": w.UUID, "entityID": w.EntityID, "entityTypeCode": w.EntityTypeCode, "err": err})
	}

	return api.Warning{
		WarningPut: api.WarningPut{
			Status: WarningStatuses[WarningStatus(w.Status)],
		},
		UUID:        w.UUID,
		Location:    w.Node,
		Project:     w.Project,
		Type:        WarningTypeNames[typeCode],
		Count:       w.Count,
		FirstSeenAt: w.FirstSeenDate,
		LastSeenAt:  w.LastSeenDate,
		LastMessage: w.LastMessage,
		Severity:    WarningSeverities[typeCode.Severity()],
		EntityURL:   entity,
	}, nil
}
