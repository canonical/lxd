package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeInstance is an instantiated Instance for convenience.
var TypeInstance = Instance{}

// TypeNameInstance is the TypeName for Instance entities.
const TypeNameInstance TypeName = "instance"

// Instance is an implementation of Type for Instance entities.
type Instance struct{}

// RequiresProject returns true for entity type Instance.
func (t Instance) RequiresProject() bool {
	return true
}

// Name returns entity.TypeNameInstance.
func (t Instance) Name() TypeName {
	return TypeNameInstance
}

// PathTemplate returns the path template for entity type Instance.
func (t Instance) PathTemplate() []string {
	return []string{"instances", pathPlaceholder}
}

// URL returns a URL for entity type Instance.
func (t Instance) URL(projectName string, instanceName string) *api.URL {
	return urlMust(t, projectName, "", instanceName)
}

// String implements fmt.Stringer for Instance entities.
func (t Instance) String() string {
	return string(t.Name())
}
