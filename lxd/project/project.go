package project

import (
	"fmt"
	"strings"
)

// Default is the string used for a default project.
const Default = "default"

// separator is used to delimit the project name from the suffix.
const separator = "_"

// Instance adds the "<project>_" prefix to instance name when the given project name is not "default".
func Instance(projectName string, instanceName string) string {
	if projectName != Default {
		return fmt.Sprintf("%s%s%s", projectName, separator, instanceName)
	}

	return instanceName
}

// InstanceParts takes a project prefixed Instance name string and returns the project and instance name.
// If a non-project prefixed Instance name is supplied, then the project is returned as "default" and the instance
// name is returned unmodified in the 2nd return value. This is suitable for passing back into Prefix().
// Note: This should only be used with Instance names (because they cannot contain the project separator) and this
// function relies on this rule as project names can contain the project separator.
func InstanceParts(projectInstanceName string) (string, string) {
	i := strings.LastIndex(projectInstanceName, separator)
	if i < 0 {
		// This string is not project prefixed or is part of default project.
		return Default, projectInstanceName
	}

	// As project names can container separator, we effectively split once from the right hand side as
	// Instance names are not allowed to container the separator value.
	return projectInstanceName[0:i], projectInstanceName[i+1:]
}

// StorageVolume adds the "<project>_prefix" to the storage volume name. Even if the project name is "default".
func StorageVolume(projectName string, storageVolumeName string) string {
	return fmt.Sprintf("%s%s%s", projectName, separator, storageVolumeName)
}
