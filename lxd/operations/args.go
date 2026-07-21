package operations

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
)

// InputKey is used to get and set operation inputs. It is a string alias type to encourage const usage (like for context keys).
type InputKey string

// OperationArgs contains all the arguments for operation creation.
type OperationArgs struct {
	// ProjectName is the name of the project the operation is "running in".
	// It namespaces the operation so that e.g. a user with `can_view_operations` on the project is able to view it.
	// It should be left empty for server operations e.g. background tasks that span projects.
	ProjectName string

	// Type is the operation type. This is used to encapsulate general operation details including the API description.
	Type operationtype.Type

	// Class describes the kind of operation that is running.
	Class operationtype.Class

	// EntityURL is URL of the primary entity for the operation. For example, if updating an instance, it should be the
	// instance URL. The [entity.Type] corresponding to the URL must equal the result of [operationtype.Type.EntityType]
	// for Type. This is so that the URL can be reconstructed the database record (where it is saved as an entity ID).
	// This field may only be unset if the result of [operationtype.Type.EntityType] for Type is [entity.TypeServer],
	// in which case, the entity URL for the operation will be set to the server url (/1.0).
	EntityURL *api.URL

	// Resources are a map of entity type to URL. This structure is for backwards compatibility.
	// All given URLs must match their corresponding entity type and must be resources that exist in the database.
	// This corresponds to the `operations_resources` table, whose intent is to allow logic that restricts concurrent
	// manipulations of a single resource. This functionality is not yet in use, so this field should not generally be
	// set.
	Resources map[entity.Type][]api.URL

	// Metadata is the operation metadata. It may contain e.g. operation secrets. For operations whose entity type is not
	// "server", the primary entity URL will be set in the api.MetadataEntityURL field (unless set by the caller). This may be nil.
	Metadata map[string]any

	// RunHook is the function that runs when the operation is scheduled. Token operations may not have a RunHook.
	RunHook RunHook

	// ConnectHook is the function that runs when a client calls /1.0/operations/{id}/websocket. It is used for instance
	// exec and migrations. Only websocket operations can have a ConnectHook.
	ConnectHook func(op *Operation, r *http.Request, w http.ResponseWriter) error

	// ConflictReference is used to prevent other operations with the same conflict reference from running.
	// It is not valid to provide a conflict reference if the Type has [operationtype.ConflictActionNone].
	ConflictReference string

	// Stage defines ordering of child operations. It is not valid to set Stage > 0 on operations that are not children.
	// Child stages must be consecutive, starting at zero. This is an uint16 to indicate that it is a low positive integer.
	Stage uint16

	// inputs are used by durable operations to give the statically defined RunHook access to caller context.
	// These values are saved to the database should the operation be relocated.
	// Values must be set via [OperationArgs.SetInputValue].
	inputs map[InputKey]json.RawMessage

	// Children are sub-operations of a bulk operation. It is not valid to provide children if [operationtype.Type.IsBulk]
	// returns false for the Type.
	Children []*OperationArgs

	// requestor represents the "owner" of the operation and is set on calls to ScheduleUserOperationFromRequest or
	// ScheduleUserOperationFromOperation. It is not set on calls to ScheduleServerOperation.
	requestor *request.RequestorAuditor

	// metricsCallback is a function that is called when an operation completes. This is only set on calls to
	// ScheduleUserOperationFromRequest and is used to update ongoing request metrics.
	metricsCallback func(result metrics.RequestResult)
}

// validate returns an error if the [OperationArgs] are invalid.
func (a OperationArgs) validate(isChild bool) error {
	err := a.Class.Validate()
	if err != nil {
		return err
	}

	err = operationtype.Validate(a.Type)
	if err != nil {
		return err
	}

	// Validate that the primary entity URL matches the operation entity type to ensure that the operation entity URL
	// can be reconstructed from a database record (where it is saved as an entity ID).
	operationEntityType := a.Type.EntityType()
	if a.EntityURL != nil {
		entityType, _, _, _, err := entity.ParseURL(a.EntityURL.URL)
		if err != nil {
			return fmt.Errorf("Invalid operation entity URL: %w", err)
		}

		if entityType != operationEntityType {
			return fmt.Errorf("Entity type for URL %q does not match operation entity type %q", a.EntityURL, operationEntityType)
		}
	} else if operationEntityType != entity.TypeServer {
		return errors.New("Operation entity URL required")
	}

	isBulkOperation := a.Type.IsBulk()

	// Child operations cannot be bulk operations (they can't be nested).
	if isChild && isBulkOperation {
		return errors.New("Bulk operations cannot have nested bulk operations")
	}

	if isBulkOperation && len(a.Children) == 0 {
		return errors.New("Bulk operations must have children")
	}

	if !isBulkOperation && len(a.Children) > 0 {
		return fmt.Errorf("Child operations not allowed for operation type %q", a.Type.Description())
	}

	switch a.Class {
	case operationtype.OperationClassTask:
		// If this is a single task operation without children, it must have a run hook.
		if len(a.Children) == 0 && a.RunHook == nil {
			return errors.New("Task operations must have a Run hook")
		}

	case operationtype.OperationClassWebsocket:
		if a.ConnectHook == nil {
			return errors.New("Websocket operations must have a Connect hook")
		}

	case operationtype.OperationClassToken:
		if a.RunHook != nil {
			return errors.New("Token operations cannot have a Run hook")
		}

	case operationtype.OperationClassDurable:
		if a.RunHook != nil {
			return errors.New("Durable operation Run hooks are statically defined")
		}
	}

	if a.Class != operationtype.OperationClassWebsocket && a.ConnectHook != nil {
		return errors.New("Only websocket operations can have a Connect hook")
	}

	if !a.Class.SupportsBulkOperations() && isBulkOperation {
		return fmt.Errorf("Operations of class %q cannot have children", a.Class.String())
	}

	if !a.Class.SupportsBulkOperations() && isChild {
		return fmt.Errorf("Operations of class %q cannot have a parent operation", a.Class.String())
	}

	if a.ConflictReference != "" && a.Type.ConflictAction() == operationtype.ConflictActionNone {
		return fmt.Errorf("Conflict reference %q provided for operation type %q that does not support conflicts", a.ConflictReference, a.Type.Description())
	}

	if !isChild && a.Stage > 0 {
		return errors.New("Only child operations have stages")
	}

	// Create a set of stages for validation.
	stageSet := make(map[uint16]struct{})
	for i, child := range a.Children {
		if child == nil {
			return errors.New("Operation children cannot be nil")
		}

		if child.ProjectName != a.ProjectName {
			return errors.New("Child operations cannot have a different project to the parent operation")
		}

		err := child.validate(true)
		if err != nil {
			return fmt.Errorf(`Failed validating child operation "%d": %w`, i, err)
		}

		stageSet[child.Stage] = struct{}{}
	}

	// Get a sorted slice of stages. It must be [0, 1, 2, ...].
	stages := slices.Collect(maps.Keys(stageSet))
	slices.Sort(stages)
	for i := range stages {
		if stages[i] != uint16(i) {
			return errors.New("Child operation stages must be consecutive, starting at 0")
		}
	}

	return nil
}

// SetInputValue sets the given value on the operation inputs. This enforces that the value can be serialized.
// Values can be retrieved via [GetOperationInputValue].
func (a *OperationArgs) SetInputValue(key InputKey, value any) error {
	if a.inputs == nil {
		a.inputs = map[InputKey]json.RawMessage{}
	}

	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("Failed setting operation input value: %w", err)
	}

	a.inputs[key] = b
	return nil
}
