//go:build linux && cgo && !agent

package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/shared/api"
)

func registerDBOperation(op *Operation) error {
	if op.state == nil {
		return nil
	}

	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Conflict reference should only be provided for operation types that support conflicts.
		if op.dbOpType.ConflictAction() == operationtype.ConflictActionNone && op.conflictReference != "" {
			return fmt.Errorf("Conflict reference %q provided for operation type %q that does not support conflicts", op.conflictReference, op.dbOpType.Description())
		}

		opInfo := cluster.Operation{
			UUID:              op.id,
			Type:              op.dbOpType,
			NodeID:            tx.GetNodeID(),
			Class:             (int64)(op.class),
			CreatedAt:         op.createdAt,
			UpdatedAt:         op.updatedAt,
			Status:            int64(op.Status()),
			ConflictReference: op.conflictReference,
		}

		if op.projectName != "" {
			projectID, err := cluster.GetProjectID(ctx, tx.Tx(), op.projectName)
			if err != nil {
				return fmt.Errorf("Fetch project ID: %w", err)
			}

			opInfo.ProjectID = &projectID
		}

		if op.requestor != nil {
			// If there is no requestor (eg. for server operations), we leave the requestor_protocol
			// and requestor_identity_id fields `null` in the database.
			// If there's an untrusted requestor with empty protocol and no identity, we set the
			// requestor_protocol to `requestorProtocolNone` and leave the requestor_identity_id `null`.
			// The untrusted requestor is provided eg. in a local image upload operation run as part of an image copy operation.
			value := cluster.RequestorProtocol(op.requestor.CallerProtocol())
			opInfo.RequestorProtocol = &value

			requestorCallerIdentityID := op.requestor.CallerIdentityID()
			if requestorCallerIdentityID != 0 {
				identityID := int64(requestorCallerIdentityID)
				opInfo.RequestorIdentityID = &identityID
			}
		}

		if op.entityURL != nil {
			entityReference, err := cluster.GetEntityReferenceFromURL(ctx, tx.Tx(), op.entityURL)
			if err != nil {
				return fmt.Errorf("Failed fetching entity reference: %w", err)
			}

			if entityReference.EntityType != cluster.EntityType(op.dbOpType.EntityType()) {
				return fmt.Errorf("Entity type %q does not match operation type's entity type %q", entityReference.EntityType, op.dbOpType.EntityType())
			}

			opInfo.EntityID = entityReference.EntityID
		}

		inputsJSON, err := json.Marshal(op.inputs)
		if err != nil {
			return fmt.Errorf("Failed marshalling operation inputs: %w", err)
		}

		opInfo.Inputs = string(inputsJSON)

		metadataJSON, err := json.Marshal(op.metadata)
		if err != nil {
			return fmt.Errorf("Failed marshalling operation metadata: %w", err)
		}

		opInfo.Metadata = string(metadataJSON)

		dbOpID, err := cluster.CreateOperation(ctx, tx.Tx(), opInfo)
		if err != nil {
			// The operations table has unique index on uuid, and confiditional unique index on conflict_reference.
			// Conflict on generated uuid is higly unlikely, so conflicts will most likely happen due to conflict on conflict_reference.
			// If that is the case, we return a more specific error message.
			if op.conflictReference != "" && api.StatusErrorCheck(err, http.StatusConflict) {
				return api.NewStatusError(http.StatusConflict, "An operation with this conflict reference is already running")
			}

			return err
		}

		err = cluster.CreateOperationResources(ctx, tx.Tx(), dbOpID, op.resources)
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		return fmt.Errorf("Failed creating %q operation record: %w", op.dbOpType.Description(), err)
	}

	return nil
}

func updateDBOperation(ctx context.Context, op *Operation) error {
	err := op.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		metadataJSON, err := json.Marshal(op.metadata)
		if err != nil {
			return fmt.Errorf("Failed marshalling operation metadata: %w", err)
		}

		return cluster.UpdateOperation(ctx, tx.Tx(), op.id, op.updatedAt, op.status, string(metadataJSON), op.err)
	})
	if err != nil {
		return fmt.Errorf("Failed updating operation %q record: %w", op.id, err)
	}

	return nil
}

func removeDBOperation(op *Operation) error {
	if op.state == nil {
		return nil
	}

	err := op.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.DeleteOperation(ctx, tx.Tx(), op.id)
	})

	return err
}

func (op *Operation) sendEvent(eventMessage any) {
	if op.events == nil {
		return
	}

	_ = op.events.Send(op.projectName, api.EventTypeOperation, eventMessage)
}
