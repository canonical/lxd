package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeProject is an instantiated Project for convenience.
var TypeProject = Project{}

// TypeNameProject is the TypeName for Project entities.
const TypeNameProject TypeName = "project"

// Project is an implementation of Type for Project entities.
type Project struct{}

// RequiresProject returns false for entity type Project.
func (t Project) RequiresProject() bool {
	return false
}

// Name returns entity.TypeNameProject.
func (t Project) Name() TypeName {
	return TypeNameProject
}

// PathTemplate returns the path template for entity type Project.
func (t Project) PathTemplate() []string {
	return []string{"projects", pathPlaceholder}
}

// URL returns a URL for entity type Project.
func (t Project) URL(projectName string) *api.URL {
	return urlMust(t, "", "", projectName)
}

// String implements fmt.Stringer for Project entities.
func (t Project) String() string {
	return string(t.Name())
}
