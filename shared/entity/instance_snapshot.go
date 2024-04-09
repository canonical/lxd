package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeInstanceSnapshot is an instantiated InstanceSnapshot for convenience.
var TypeInstanceSnapshot = InstanceSnapshot{}

// TypeNameInstanceSnapshot is the TypeName for InstanceSnapshot entities.
const TypeNameInstanceSnapshot TypeName = "instance_snapshot"

// InstanceSnapshot is an implementation of Type for InstanceSnapshot entities.
type InstanceSnapshot struct{}

// RequiresProject returns true for entity type InstanceSnapshot.
func (t InstanceSnapshot) RequiresProject() bool {
	return true
}

// Name returns entity.TypeNameInstanceSnapshot.
func (t InstanceSnapshot) Name() TypeName {
	return TypeNameInstanceSnapshot
}

// PathTemplate returns the path template for entity type InstanceSnapshot.
func (t InstanceSnapshot) PathTemplate() []string {
	return []string{"instances", pathPlaceholder, "snapshots", pathPlaceholder}
}

// URL returns a URL for entity type InstanceSnapshot.
func (t InstanceSnapshot) URL(projectName string, instanceName string, instanceSnapshotName string) *api.URL {
	return urlMust(t, projectName, "", instanceName, instanceSnapshotName)
}

// String implements fmt.Stringer for InstanceSnapshot entities.
func (t InstanceSnapshot) String() string {
	return string(t.Name())
}
