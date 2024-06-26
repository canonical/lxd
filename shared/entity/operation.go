package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeOperation is an instantiated Operation for convenience.
var TypeOperation = Operation{}

// TypeNameOperation is the TypeName for Operation entities.
const TypeNameOperation TypeName = "operation"

// Operation is an implementation of Type for Operation entities.
type Operation struct{}

// RequiresProject returns false for entity type Operation.
func (t Operation) RequiresProject() bool {
	return false
}

// Name returns entity.TypeNameOperation.
func (t Operation) Name() TypeName {
	return TypeNameOperation
}

// PathTemplate returns the path template for entity type Operation.
func (t Operation) PathTemplate() []string {
	return []string{"operations", pathPlaceholder}
}

// URL returns a URL for entity type Operation.
func (t Operation) URL(operationUUID string) *api.URL {
	return urlMust(t, "", "", operationUUID)
}

// String implements fmt.Stringer for Operation entities.
func (t Operation) String() string {
	return string(t.Name())
}
