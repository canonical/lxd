package entity

import (
	"github.com/canonical/lxd/shared/api"
)

// TypeInstanceBackup is an instantiated InstanceBackup for convenience.
var TypeInstanceBackup = InstanceBackup{}

// TypeNameInstanceBackup is the TypeName for InstanceBackup entities.
const TypeNameInstanceBackup TypeName = "instance_backup"

// InstanceBackup is an implementation of Type for InstanceBackup entities.
type InstanceBackup struct{}

// RequiresProject returns true for entity type InstanceBackup.
func (t InstanceBackup) RequiresProject() bool {
	return true
}

// Name returns entity.TypeNameInstanceBackup.
func (t InstanceBackup) Name() TypeName {
	return TypeNameInstanceBackup
}

// PathTemplate returns the path template for entity type InstanceBackup.
func (t InstanceBackup) PathTemplate() []string {
	return []string{"instances", pathPlaceholder, "backups", pathPlaceholder}
}

// URL returns a URL for entity type InstanceBackup.
func (t InstanceBackup) URL(projectName string, instanceName string, instanceBackupName string) *api.URL {
	return urlMust(t, projectName, "", instanceName, instanceBackupName)
}

// String implements fmt.Stringer for InstanceBackup entities.
func (t InstanceBackup) String() string {
	return string(t.Name())
}
