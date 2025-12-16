//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t operations.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e operation objects
//go:generate mapper stmt -e operation objects-by-NodeID
//go:generate mapper stmt -e operation objects-by-ID
//go:generate mapper stmt -e operation objects-by-UUID
//go:generate mapper stmt -e operation create
//go:generate mapper stmt -e operation create-or-replace
//go:generate mapper stmt -e operation delete-by-UUID
//go:generate mapper stmt -e operation delete-by-NodeID
//
//go:generate mapper method -i -e operation GetMany
//go:generate mapper method -i -e operation Create
//go:generate mapper method -i -e operation CreateOrReplace
//go:generate mapper method -i -e operation DeleteOne-by-UUID
//go:generate mapper method -i -e operation DeleteMany-by-NodeID
//go:generate goimports -w operations.mapper.go
//go:generate goimports -w operations.interface.mapper.go

// Operation holds information about a single LXD operation running on a node
// in the cluster.
type Operation struct {
	ID                  int64              `db:"primary=yes"`                                                      // Stable database identifier
	UUID                string             `db:"primary=yes"`                                                      // User-visible identifier, such as uuid
	NodeAddress         string             `db:"coalesce=''&leftjoin=nodes.address&omit=create,create-or-replace"` // Address of the node the operation is running on
	ProjectID           *int64             // ID of the project for the operation.
	NodeID              int64              // ID of the node the operation is running on
	Type                operationtype.Type // Type of the operation
	RequestorProtocol   *RequestorProtocol // Protocol from the operation requestor
	RequestorIdentityID *int64             // Identity ID from the operation requestor
	EntityID            int                // ID of the entity the operation acts upon
	Metadata            string             // JSON encoded metadata for the operation
	Class               int64              // Class of the operation
	CreatedAt           time.Time          // Time the operation was created
	UpdatedAt           time.Time          // Time when the state or the metadata of the operation were last updated
	Inputs              string             // JSON encoded inputs for the operation
	Status              int64              `db:"sql=operations.status_code"` // Status code of the operation
	ConflictReference   string             // All operations which cannot run concurrently share the same conflict reference
	Error               string             // Error message if the operation failed
	Parent              *int64             // Parent operation ID. This is used for sub-operations of other operations.
	Stage               int64              // Stage of the operation
}

// OperationFilter specifies potential query parameter fields.
type OperationFilter struct {
	ID     *int64
	NodeID *int64
	UUID   *string
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

// requestorProtocolTextToCode maps RequestorProtocol string constants to their int64 database representation.
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
		return fmt.Errorf("Unknown requestor protocol `%d`", requestorProtocolCode)
	}

	*r = RequestorProtocol(text)
	return nil
}

// Scan implements [sql.Scanner] for [RequestorProtocol]. This converts the integer value back into the correct constant or
// returns an error.
func (r *RequestorProtocol) Scan(value any) error {
	return query.ScanValue(value, r, false)
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

// UpdateOperation updates operation status, metadata and error (if set) in the cluster db.
// This is used to keep DB in sync with the current status of the operation when the operation changes
// its status, or when calls to commit metadata explicitly.
func UpdateOperation(ctx context.Context, tx *sql.Tx, opUUID string,
	updatedAt time.Time, newStatus api.StatusCode, metadata string, opErr error) error {
	stmt := `UPDATE operations SET updated_at = ?, status_code = ?, metadata = ?, error = ? WHERE uuid = ?`

	opErrStr := ""
	if opErr != nil {
		opErrStr = opErr.Error()
	}

	result, err := tx.ExecContext(ctx, stmt, updatedAt, newStatus, metadata, opErrStr, opUUID)
	if err != nil {
		return fmt.Errorf("Failed updating operation status: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Fetch affected rows: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("Query updated %d rows instead of 1", n)
	}

	return nil
}
