//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// OperationsRow is a row of the operations table.
// db:model operations
type OperationsRow struct {
	ID int64 `db:"id"`

	// db:omit update
	UUID   string `db:"uuid"`
	NodeID int64  `db:"node_id"`

	// db:omit update
	Type operationtype.Type `db:"type"`

	// db:omit update
	ProjectID *int64 `db:"project_id"`

	// db:omit update
	RequestorProtocol *RequestorProtocol `db:"requestor_protocol"`

	// db:omit update
	RequestorIdentityID *int64 `db:"requestor_identity_id"`

	// db:omit update
	EntityID int64  `db:"entity_id"`
	Metadata string `db:"metadata"`

	// db:omit update
	Class int64 `db:"class"`

	// db:omit update
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`

	// db:omit update
	Inputs     string `db:"inputs"`
	StatusCode int64  `db:"status_code"`
	Error      string `db:"error"`

	// db:omit update
	ConflictReference string `db:"conflict_reference"`

	// db:omit update
	Parent *int64 `db:"parent"`

	// db:omit update
	Stage     int64 `db:"stage"`
	ErrorCode int64 `db:"error_code"`
}

// APIName implements [query.APINamer] for [OperationsRow] for API friendly error messages.
func (OperationsRow) APIName() string {
	return "Operation"
}

// Operation enriches an OperationsRow with project, node, and identity information.
// db:model operations
type Operation struct {
	Row OperationsRow

	// db:join LEFT JOIN projects ON operations.project_id = projects.id
	ProjectName string `db:"coalesce(projects.name, '')"`

	// db:join JOIN nodes ON operations.node_id = nodes.id
	NodeAddress string `db:"nodes.address"`
	NodeName    string `db:"nodes.name"`

	// db:join LEFT JOIN identities ON operations.requestor_identity_id = identities.id
	IdentityIdentifier string `db:"coalesce(identities.identifier, '')"`
}

// RequestorProtocol is the database representation of the Requestor Protocol.
//
// RequestorProtocol is defined on string so that constants can be converted by casting. The [sql.Scanner] and
// [driver.Valuer] interfaces are implemented on this type such that the string constants are converted into their int64
// counterparts as they are written to the database, or converted back into an [RequestorProtocol] as they are read from the
// database. It is not possible to read/write an invalid requestor protocol from/to the database when using this type.
type RequestorProtocol string

const (
	requestorProtocolNone int64 = iota
	requestorProtocolCluster
	requestorProtocolUnix
	requestorProtocolPKI
	requestorProtocolDevLXD
	requestorProtocolTLS
	requestorProtocolOIDC
	requestorProtocolBearer
)

// requestorProtocolCodeToText maps RequestorProtocol int64 database representation to its string constant.
var requestorProtocolCodeToText = map[int64]string{
	requestorProtocolNone:    "",
	requestorProtocolCluster: request.ProtocolCluster,
	requestorProtocolUnix:    request.ProtocolUnix,
	requestorProtocolPKI:     request.ProtocolPKI,
	requestorProtocolDevLXD:  request.ProtocolDevLXD,
	requestorProtocolTLS:     api.AuthenticationMethodTLS,
	requestorProtocolOIDC:    api.AuthenticationMethodOIDC,
	requestorProtocolBearer:  api.AuthenticationMethodBearer,
}

// ScanInteger implements [query.IntegerScanner] for [RequestorProtocol]. This simplifies the Scan implementation.
func (r *RequestorProtocol) ScanInteger(requestorProtocolCode int64) error {
	text, ok := requestorProtocolCodeToText[requestorProtocolCode]
	if !ok {
		return fmt.Errorf("Unknown requestor protocol %d", requestorProtocolCode)
	}

	*r = RequestorProtocol(text)
	return nil
}

// Scan implements [sql.Scanner] for [RequestorProtocol]. This converts the integer value back into the correct constant or
// returns an error.
func (r *RequestorProtocol) Scan(value any) error {
	// We set allowNull to true here, but it doesn't really have any effect.
	// The SQL layer doesn't call the Scan method if the value is NULL, so we would never get a NULL value here.
	// If the value is NULL, the *RequestorProtocol is just not set and remains nil.
	// This is the desired behavior, as a NULL value in the database corresponds to a missing requestor protocol.
	return query.ScanValue(value, r, true)
}

// Value implements [driver.Valuer] for [RequestorProtocol]. This converts the API constant into an integer or throws an error.
func (r *RequestorProtocol) Value() (driver.Value, error) {
	if r == nil {
		return nil, nil
	}

	switch *r {
	case "":
		return requestorProtocolNone, nil
	case RequestorProtocol(request.ProtocolCluster):
		return requestorProtocolCluster, nil
	case RequestorProtocol(request.ProtocolUnix):
		return requestorProtocolUnix, nil
	case RequestorProtocol(request.ProtocolPKI):
		return requestorProtocolPKI, nil
	case RequestorProtocol(request.ProtocolDevLXD):
		return requestorProtocolDevLXD, nil
	case RequestorProtocol(api.AuthenticationMethodTLS):
		return requestorProtocolTLS, nil
	case RequestorProtocol(api.AuthenticationMethodOIDC):
		return requestorProtocolOIDC, nil
	case RequestorProtocol(api.AuthenticationMethodBearer):
		return requestorProtocolBearer, nil
	}

	return nil, fmt.Errorf("Invalid requestor protocol %q", *r)
}

// OperationsResourcesRow represents a row of the operations_resources table.
// db:model operations_resources
type OperationsResourcesRow struct {
	// db:primary
	OperationID int64 `db:"operation_id"`
	// db:primary
	EntityID int64 `db:"entity_id"`
	// db:primary
	EntityType EntityType `db:"entity_type"`
}

// APIName implements [query.APINamer] for [OperationsResourcesRow] for API friendly error messages.
func (OperationsResourcesRow) APIName() string {
	return "Operation resource"
}

// GetOperations returns slice of [Operation] based on given filters. If includeChildren is false, only operations that
// do not reference parent operations are returned. If the project is non-nil, only operations in the given project are
// returned.
func GetOperations(ctx context.Context, tx *sql.Tx, includeChildren bool, project *string) ([]Operation, error) {
	clauses := make([]string, 0, 2)
	args := make([]any, 0, 1)
	if !includeChildren {
		clauses = append(clauses, "operations.parent IS NULL")
	}

	if project != nil {
		clauses = append(clauses, "projects.name = ?")
		args = append(args, *project)
	}

	clause := ""
	if len(clauses) > 0 {
		clause = "WHERE " + strings.Join(clauses, " AND ")
	}

	return query.Select[Operation](ctx, tx, clause, args...)
}

// UpdateOperation updates operation status, metadata and error (if set) in the cluster db.
// This is used to keep DB in sync with the current status of the operation when the operation changes
// its status, or when calls to commit metadata explicitly. The caller is expected to pass in the current node ID.
// This guards against modification of the operation by other cluster members.
func UpdateOperation(ctx context.Context, tx *sql.Tx, opUUID string, nodeID int64, updatedAt time.Time, newStatus api.StatusCode, metadata map[string]any, opErr string, opErrCode int64) error {
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("Failed marshalling operation metadata: %w", err)
	}

	// Set the updatable fields (see OperationsRow struct definition field comments).
	row := OperationsRow{
		UUID:       opUUID,
		NodeID:     nodeID,
		Metadata:   string(metadataJSON),
		UpdatedAt:  updatedAt,
		StatusCode: int64(newStatus),
		Error:      opErr,
		ErrorCode:  opErrCode,
	}

	// Only update if the operation is on the specified cluster member.
	err = query.UpdateOne(ctx, tx, row, "WHERE operations.uuid = ? AND operations.node_id = ?", opUUID, nodeID)
	if err != nil {
		// For the unhappy path, check if an operation exists with the given UUID.
		_, selectErr := query.SelectOne[OperationsRow](ctx, tx, "WHERE operations.uuid = ?", opUUID)
		if selectErr != nil {
			// If no operation exists with the UUID (or the SELECT fails), return the original error.
			return err
		}

		// Otherwise, the operation exists but is not present on the specified node.
		// Return a conflict error so that the caller can detect this.
		return api.StatusErrorf(http.StatusConflict, `Operation %q is not present on cluster member "%d"`, opUUID, nodeID)
	}

	return nil
}

// CreateOperationResources registers operation resources in the cluster db.
func CreateOperationResources(ctx context.Context, tx *sql.Tx, opID int64, resources map[entity.Type][]api.URL) error {
	// No resources to register.
	if len(resources) == 0 {
		return nil
	}

	var opResources []OperationsResourcesRow
	for _, entityURLs := range resources {
		for _, entityURL := range entityURLs {
			entityReference, err := GetEntityReferenceFromURL(ctx, tx, &entityURL)
			if err != nil {
				return fmt.Errorf("Failed getting entity ID from resource URL %q: %w", entityURL.String(), err)
			}

			opResources = append(opResources, OperationsResourcesRow{
				OperationID: opID,
				EntityID:    int64(entityReference.EntityID),
				EntityType:  entityReference.EntityType,
			})
		}
	}

	return query.CreateMany(ctx, tx, opResources)
}

// deleteEphemeralOperationsFromNodes deletes ephemeral operations from nodes with the given list of IDs.
// Ephemeral operations are operations which are normally cleared few seconds after they finish. In other words, these are:
// - Operations with class Task, Websocket or Token (class between 1 and 3), and
// - Operations which are not bulk operations (parent is NULL and id not in parent column of any operation).
func deleteEphemeralOperationsFromNodes(ctx context.Context, tx *sql.Tx, nodeIDs ...int64) error {
	stmt := `DELETE FROM operations
WHERE class BETWEEN 1 AND 3
AND parent IS NULL
AND id NOT IN (SELECT parent FROM operations WHERE parent IS NOT NULL)
AND node_id IN ` + query.IntParams(nodeIDs...)

	_, err := tx.ExecContext(ctx, stmt)
	if err != nil {
		return fmt.Errorf("Failed deleting operations from nodes: %w", err)
	}

	return nil
}

// failRunningBulkOperationsFromNodes marks all running bulk operations on target nodes as failed.
// Bulk operations are persisted in the database for 24 hours, thus are not ephemeral. Yet, if a node crashes or shuts down,
// the bulk operations which were not running on the node previously are not going to finish and need to be marked accordingly.
func failRunningBulkOperationsFromNodes(ctx context.Context, tx *sql.Tx, nodeIDs ...int64) error {
	stmt := `UPDATE operations SET updated_at = ?, status_code = ?, error = ?, error_code = ?
WHERE class BETWEEN 1 AND 3
AND status_code < 200
AND (parent IS NOT NULL OR id IN (SELECT parent FROM operations WHERE parent IS NOT NULL))
AND node_id IN ` + query.IntParams(nodeIDs...)

	_, err := tx.ExecContext(ctx, stmt, time.Now(), api.Failure, "Member shut down", http.StatusServiceUnavailable)
	if err != nil {
		return fmt.Errorf("Failed marking bulk operations as failed: %w", err)
	}

	return nil
}

// ClearStaleOperationsFromNodes clears all stale operation records from the database for the given node IDs. This includes:
// - Deleting ephemeral operations, which are operations that are normally cleared few seconds after they finish.
// - Marking running bulk operations as failed.
func ClearStaleOperationsFromNodes(ctx context.Context, tx *sql.Tx, nodeIDs ...int64) error {
	err := deleteEphemeralOperationsFromNodes(ctx, tx, nodeIDs...)
	if err != nil {
		return fmt.Errorf("Failed deleting ephemeral operations from nodes: %w", err)
	}

	err = failRunningBulkOperationsFromNodes(ctx, tx, nodeIDs...)
	if err != nil {
		return fmt.Errorf("Failed failing running bulk operations from nodes: %w", err)
	}

	return nil
}

// GetOperationsByProjectAndType returns a slice of [Operation] with the given project and type.
func GetOperationsByProjectAndType(ctx context.Context, tx *sql.Tx, projectName string, opType operationtype.Type) ([]Operation, error) {
	return query.Select[Operation](ctx, tx, "WHERE coalesce(projects.name, '') = ? AND operations.type = ?", projectName, opType)
}

// DeleteOperation deletes an operation by UUID.
func DeleteOperation(ctx context.Context, tx *sql.Tx, operationUUID string) error {
	return query.DeleteOne[OperationsRow](ctx, tx, "WHERE operations.uuid = ?", operationUUID)
}

// GetOperation gets an [Operation] by UUID.
func GetOperation(ctx context.Context, tx *sql.Tx, operationUUID string) (*Operation, error) {
	return query.SelectOne[Operation](ctx, tx, "WHERE operations.uuid = ?", operationUUID)
}

// GetOperationsWithParent gets all operations whose parent operation has the given ID.
func GetOperationsWithParent(ctx context.Context, tx *sql.Tx, parentID int64) ([]Operation, error) {
	return query.Select[Operation](ctx, tx, "WHERE operations.parent = ?", parentID)
}

// GetOperationsByNodeID gets all operations on the given node ID.
func GetOperationsByNodeID(ctx context.Context, tx *sql.Tx, nodeID int64) ([]Operation, error) {
	return query.Select[Operation](ctx, tx, "WHERE operations.node_id = ?", nodeID)
}

// CountOperationChildrenByParent returns a map of parent operation UUID to child count.
// This is used to populate ChildCount on list responses without loading full child operations.
func CountOperationChildrenByParent(ctx context.Context, tx *sql.Tx) (map[string]int64, error) {
	stmt := `
		SELECT parent_op.uuid, COUNT(*)
		FROM operations child_op
		JOIN operations parent_op ON child_op.parent = parent_op.id
		WHERE child_op.parent IS NOT NULL
		GROUP BY parent_op.id`

	counts := make(map[string]int64)
	err := query.Scan(ctx, tx, stmt, func(scan func(dest ...any) error) error {
		var uuid string
		var count int64
		err := scan(&uuid, &count)
		if err != nil {
			return err
		}

		counts[uuid] = count
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Failed counting child operations by parent: %w", err)
	}

	return counts, nil
}

// CountOperationChildren returns the number of child operations for the given parent operation DB ID.
func CountOperationChildren(ctx context.Context, tx *sql.Tx, parentID int64) (int64, error) {
	stmt := `SELECT COUNT(*) FROM operations WHERE parent = ?`

	var count int64
	err := query.Scan(ctx, tx, stmt, func(scan func(dest ...any) error) error {
		return scan(&count)
	}, parentID)
	if err != nil {
		return 0, fmt.Errorf("Failed counting child operations: %w", err)
	}

	return count, nil
}
