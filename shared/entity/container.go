package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeContainer is an instantiated Container for convenience.
var TypeContainer = Container{}

// TypeNameContainer is the TypeName for Container entities.
const TypeNameContainer TypeName = "container"

// Container is an implementation of Type for Container entities.
type Container struct{}

// RequiresProject returns true for entity type Container.
func (t Container) RequiresProject() bool {
	return true
}

// Name returns entity.TypeNameContainer.
func (t Container) Name() TypeName {
	return TypeNameContainer
}

// PathTemplate returns the path template for entity type Container.
func (t Container) PathTemplate() []string {
	return []string{"containers", pathPlaceholder}
}

// URL returns a URL for entity type Container.
func (t Container) URL(projectName string, containerName string) *api.URL {
	return urlMust(t, projectName, "", containerName)
}

// String implements fmt.Stringer for Container entities.
func (t Container) String() string {
	return string(t.Name())
}
