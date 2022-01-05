//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

import (
	"fmt"
	"time"

	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

// Warning is a value object holding db-related details about a warning.
type Warning struct {
	ID             int
	Node           string
	Project        string
	EntityTypeCode int
	EntityID       int
	UUID           string
	TypeCode       WarningType
	Status         WarningStatus
	FirstSeenDate  time.Time
	LastSeenDate   time.Time
	UpdatedDate    time.Time
	LastMessage    string
	Count          int
}

var warningCreate = cluster.RegisterStmt(`
INSERT INTO warnings (node_id, project_id, entity_type_code, entity_id, uuid, type_code, status, first_seen_date, last_seen_date, updated_date, last_message, count)
  VALUES ((SELECT nodes.id FROM nodes WHERE nodes.name = ?), (SELECT projects.id FROM projects WHERE projects.name = ?), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`)

var warningDelete = cluster.RegisterStmt(`
DELETE FROM warnings WHERE uuid = ?
`)

var warningDeleteByStatus = cluster.RegisterStmt(`
DELETE FROM warnings WHERE status = ?
`)

var warningID = cluster.RegisterStmt(`
SELECT warnings.id FROM warnings LEFT JOIN nodes ON warnings.node_id = nodes.id LEFT JOIN projects ON warnings.project_id = projects.id
  WHERE warnings.uuid = ?
`)

// UpsertWarningLocalNode creates or updates a warning for the local node. Returns error if no local node name.
func (c *Cluster) UpsertWarningLocalNode(projectName string, entityTypeCode int, entityID int, typeCode WarningType, message string) error {
	var err error
	var localName string

	err = c.Transaction(func(tx *ClusterTx) error {
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

	return c.UpsertWarning(localName, projectName, entityTypeCode, entityID, typeCode, message)

}

// UpsertWarning creates or updates a warning.
func (c *Cluster) UpsertWarning(nodeName string, projectName string, entityTypeCode int, entityID int, typeCode WarningType, message string) error {
	// Validate
	_, err := c.GetURIFromEntity(entityTypeCode, entityID)
	if err != nil {
		return errors.Wrapf(err, "Failed to get URI for entity ID %d with entity type code %d", entityID, entityTypeCode)
	}

	_, ok := WarningTypeNames[typeCode]
	if !ok {
		return fmt.Errorf("Unknown warning type code %d", typeCode)
	}

	now := time.Now()

	err = c.Transaction(func(tx *ClusterTx) error {
		allWarnings, err := tx.GetWarnings()
		if err != nil {
			return errors.Wrap(err, "Failed to retrieve warnings")
		}

		var warnings []Warning

		// Check if one of the warnings match. If so, it will be updated. Otherwise it will be
		// created.
		for _, w := range allWarnings {
			if !(w.EntityID == entityID && w.EntityTypeCode == entityTypeCode && typeCode == WarningType(w.TypeCode) && projectName == w.Project && nodeName == w.Node) {
				continue
			}

			warnings = append(warnings, w)
		}

		if len(warnings) > 1 {
			// This shouldn't happen
			return fmt.Errorf("Too many warnings match the criteria")
		}

		if len(warnings) == 1 {
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
	str := fmt.Sprintf("UPDATE warnings SET status=?, updated_date=? WHERE uuid=?")
	stmt, err := c.tx.Prepare(str)
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(status, time.Now(), UUID)
	if err != nil {
		return errors.Wrapf(err, "Failed to update warning status for warning %q", UUID)
	}

	return nil
}

// UpdateWarningState updates the warning message and status with the given ID.
func (c *ClusterTx) UpdateWarningState(UUID string, message string, status WarningStatus) error {
	str := fmt.Sprintf("UPDATE warnings SET last_message=?, last_seen_date=?, updated_date=?, status = ?, count=count+1 WHERE uuid=?")
	stmt, err := c.tx.Prepare(str)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now()

	_, err = stmt.Exec(message, now, now, status, UUID)
	if err != nil {
		return errors.Wrapf(err, "Failed to update warning %q", UUID)
	}

	return nil
}

func (c *ClusterTx) doGetWarnings(q string, args ...interface{}) ([]Warning, error) {
	stmt, err := c.tx.Prepare(q)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	// Result slice.
	objects := make([]Warning, 0)

	// Dest function for scanning a row.
	dest := func(i int) []interface{} {
		objects = append(objects, Warning{})
		return []interface{}{
			&objects[i].ID,
			&objects[i].Node,
			&objects[i].Project,
			&objects[i].EntityTypeCode,
			&objects[i].EntityID,
			&objects[i].UUID,
			&objects[i].TypeCode,
			&objects[i].Status,
			&objects[i].FirstSeenDate,
			&objects[i].LastSeenDate,
			&objects[i].UpdatedDate,
			&objects[i].LastMessage,
			&objects[i].Count,
		}
	}

	// Select.
	err = query.SelectObjects(stmt, dest, args...)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to fetch warnings")
	}

	return objects, nil
}

// GetWarningsByType returns all available warnings with the given type.
func (c *ClusterTx) GetWarningsByType(typeCode WarningType) ([]Warning, error) {
	q := `SELECT warnings.id, IFNULL(nodes.name, "") AS node, IFNULL(projects.name, "") AS project, IFNULL(warnings.entity_type_code, -1), IFNULL(warnings.entity_id, -1), warnings.uuid, warnings.type_code, warnings.status, warnings.first_seen_date, warnings.last_seen_date, warnings.updated_date, warnings.last_message, warnings.count
	FROM warnings LEFT JOIN nodes ON warnings.node_id = nodes.id LEFT JOIN projects ON warnings.project_id = projects.id WHERE type_code=? ORDER BY warnings.last_seen_date`

	return c.doGetWarnings(q, typeCode)
}

// GetWarningsByStatus returns all available warnings with the given status.
func (c *ClusterTx) GetWarningsByStatus(status WarningStatus) ([]Warning, error) {
	q := `SELECT warnings.id, IFNULL(nodes.name, "") AS node, IFNULL(projects.name, "") AS project, IFNULL(warnings.entity_type_code, -1), IFNULL(warnings.entity_id, -1), warnings.uuid, warnings.type_code, warnings.status, warnings.first_seen_date, warnings.last_seen_date, warnings.updated_date, warnings.last_message, warnings.count
	FROM warnings LEFT JOIN nodes ON warnings.node_id = nodes.id LEFT JOIN projects ON warnings.project_id = projects.id WHERE status=? ORDER BY warnings.last_seen_date`

	return c.doGetWarnings(q, status)
}

// GetWarningsByProject returns all available warnings in the given project.
func (c *ClusterTx) GetWarningsByProject(projectName string) ([]Warning, error) {
	q := `SELECT warnings.id, IFNULL(nodes.name, "") AS node, IFNULL(projects.name, "") AS project, IFNULL(warnings.entity_type_code, -1), IFNULL(warnings.entity_id, -1), warnings.uuid, warnings.type_code, warnings.status, warnings.first_seen_date, warnings.last_seen_date, warnings.updated_date, warnings.last_message, warnings.count
	FROM warnings LEFT JOIN nodes ON warnings.node_id = nodes.id LEFT JOIN projects ON warnings.project_id = projects.id WHERE project=? ORDER BY warnings.last_seen_date`

	return c.doGetWarnings(q, projectName)
}

// GetWarnings returns all available warnings.
func (c *ClusterTx) GetWarnings() ([]Warning, error) {
	q := `SELECT warnings.id, IFNULL(nodes.name, "") AS node, IFNULL(projects.name, "") AS project, IFNULL(warnings.entity_type_code, -1), IFNULL(warnings.entity_id, -1), warnings.uuid, warnings.type_code, warnings.status, warnings.first_seen_date, warnings.last_seen_date, warnings.updated_date, warnings.last_message, warnings.count
	FROM warnings LEFT JOIN nodes ON warnings.node_id = nodes.id LEFT JOIN projects ON warnings.project_id = projects.id ORDER BY warnings.last_seen_date`

	return c.doGetWarnings(q)
}

// createWarning adds a new warning to the database.
func (c *ClusterTx) createWarning(object Warning) (int64, error) {
	// Check if a warning with the same key exists.
	exists, err := c.WarningExists(object.UUID)
	if err != nil {
		return -1, errors.Wrap(err, "Failed to check for duplicates")
	}
	if exists {
		return -1, fmt.Errorf("This warning already exists")
	}

	args := make([]interface{}, 12)

	// Populate the statement arguments.
	if object.Node != "" {
		// Ensure node exists
		_, err = c.GetNodeByName(object.Node)
		if err != nil {
			return -1, errors.Wrap(err, "Failed to get node")
		}

		args[0] = object.Node
	}

	if object.Project != "" {
		// Ensure project exists
		projects, err := c.GetProjectNames()
		if err != nil {
			return -1, errors.Wrap(err, "Failed to get project names")
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
		return -1, errors.Wrap(err, "Failed to create warning")
	}

	id, err := result.LastInsertId()
	if err != nil {
		return -1, errors.Wrap(err, "Failed to fetch warning ID")
	}

	return id, nil
}

// GetWarning returns the warning with the given key.
func (c *ClusterTx) GetWarning(UUID string) (*Warning, error) {
	q := `SELECT warnings.id, IFNULL(nodes.name, "") AS node, IFNULL(projects.name, "") AS project, IFNULL(warnings.entity_type_code, -1), IFNULL(warnings.entity_id, -1), warnings.uuid, warnings.type_code, warnings.status, warnings.first_seen_date, warnings.last_seen_date, warnings.updated_date, warnings.last_message, warnings.count
	FROM warnings LEFT JOIN nodes ON warnings.node_id = nodes.id LEFT JOIN projects ON warnings.project_id = projects.id WHERE uuid = ?`

	stmt, err := c.tx.Prepare(q)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	// Result slice.
	var objects []Warning

	// Dest function for scanning a row.
	dest := func(i int) []interface{} {
		objects = append(objects, Warning{})
		return []interface{}{
			&objects[i].ID,
			&objects[i].Node,
			&objects[i].Project,
			&objects[i].EntityTypeCode,
			&objects[i].EntityID,
			&objects[i].UUID,
			&objects[i].TypeCode,
			&objects[i].Status,
			&objects[i].FirstSeenDate,
			&objects[i].LastSeenDate,
			&objects[i].UpdatedDate,
			&objects[i].LastMessage,
			&objects[i].Count,
		}
	}

	// Select.
	err = query.SelectObjects(stmt, dest, UUID)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to fetch warnings")
	}

	switch len(objects) {
	case 0:
		return nil, ErrNoSuchObject
	case 1:
		return &objects[0], nil
	default:
		return nil, fmt.Errorf("More than one warning matches")
	}
}

// DeleteWarning deletes the warning matching the given key parameters.
func (c *ClusterTx) DeleteWarning(UUID string) error {
	stmt := c.stmt(warningDelete)
	result, err := stmt.Exec(UUID)
	if err != nil {
		return errors.Wrap(err, "Delete warning")
	}

	n, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "Fetch affected rows")
	}
	if n != 1 {
		return fmt.Errorf("Query deleted %d rows instead of 1", n)
	}

	return nil
}

// GetWarningID return the ID of the warning with the given key.
func (c *ClusterTx) GetWarningID(UUID string) (int64, error) {
	stmt := c.stmt(warningID)
	rows, err := stmt.Query(UUID)
	if err != nil {
		return -1, errors.Wrap(err, "Failed to get warning ID")
	}
	defer rows.Close()

	// Ensure we read one and only one row.
	if !rows.Next() {
		return -1, ErrNoSuchObject
	}
	var id int64
	err = rows.Scan(&id)
	if err != nil {
		return -1, errors.Wrap(err, "Failed to scan ID")
	}
	if rows.Next() {
		return -1, fmt.Errorf("More than one row returned")
	}
	err = rows.Err()
	if err != nil {
		return -1, errors.Wrap(err, "Result set failure")
	}

	return id, nil
}

// WarningExists checks if a warning with the given key exists.
func (c *ClusterTx) WarningExists(UUID string) (bool, error) {
	_, err := c.GetWarningID(UUID)
	if err != nil {
		if err == ErrNoSuchObject {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

// DeleteWarningsByStatus deletes all warnings with the given status.
func (c *ClusterTx) DeleteWarningsByStatus(status WarningStatus) error {
	stmt := c.stmt(warningDeleteByStatus)

	_, err := stmt.Exec(int(status))
	if err != nil {
		return errors.Wrap(err, "Delete all warning")
	}

	return nil
}

// DeleteWarningsByEntity deletes all warnings with the given entity type and entity ID.
func (c *ClusterTx) DeleteWarningsByEntity(entityTypeCode int, entityID int) error {
	_, err := c.tx.Exec(`DELETE FROM warnings WHERE entity_type_code = ? AND entity_id = ?`, entityTypeCode, entityID)
	return err
}

// ToAPI returns a LXD API entry.
func (w Warning) ToAPI(c *Cluster) (api.Warning, error) {
	typeCode := WarningType(w.TypeCode)

	entity, err := c.GetURIFromEntity(w.EntityTypeCode, w.EntityID)
	if err != nil {
		logger.Warn("Failed to get entity URI for warning", log.Ctx{"ID": w.UUID, "entityID": w.EntityID, "entityTypeCode": w.EntityTypeCode, "err": err})
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
