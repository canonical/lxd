package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeWarning is an instantiated Warning for convenience.
var TypeWarning = Warning{}

// TypeNameWarning is the TypeName for Warning entities.
const TypeNameWarning TypeName = "warning"

// Warning is an implementation of Type for Warning entities.
type Warning struct{}

// RequiresProject returns false for entity type Warning.
func (t Warning) RequiresProject() bool {
	return false
}

// Name returns entity.TypeNameWarning.
func (t Warning) Name() TypeName {
	return TypeNameWarning
}

// PathTemplate returns the path template for entity type Warning.
func (t Warning) PathTemplate() []string {
	return []string{"warnings", pathPlaceholder}
}

// URL returns a URL for entity type Warning.
func (t Warning) URL(projectName string, warningUUID string) *api.URL {
	return urlMust(t, projectName, "", warningUUID)
}

// String implements fmt.Stringer for Warning entities.
func (t Warning) String() string {
	return string(t.Name())
}
