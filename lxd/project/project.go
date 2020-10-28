package project

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
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

// DNS adds ".<project>" as a suffix to instance name when the given project name is not "default".
func DNS(projectName string, instanceName string) string {
	if projectName != Default {
		return fmt.Sprintf("%s.%s", instanceName, projectName)
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

// StorageVolumeParts takes a project prefixed storage volume name and returns the project and storage volume
// name as separate variables.
func StorageVolumeParts(projectStorageVolumeName string) (string, string) {
	parts := strings.SplitN(projectStorageVolumeName, "_", 2)
	return parts[0], parts[1]
}

// StorageVolumeProject returns the project name to use to for the volume based on the requested project.
// For custom volume type, if the project specified has the "features.storage.volumes" flag enabled then the
// project name is returned, otherwise the default project name is returned. For all other volume types the
// supplied project name is returned.
func StorageVolumeProject(c *db.Cluster, projectName string, volumeType int) (string, error) {
	// Non-custom volumes always use the project specified. Optimisation to avoid loading project record.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return projectName, nil
	}

	var project *api.Project
	var err error

	err = c.Transaction(func(tx *db.ClusterTx) error {
		project, err = tx.GetProject(projectName)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return "", errors.Wrapf(err, "Failed to load project %q", projectName)
	}

	return StorageVolumeProjectFromRecord(project, volumeType), nil
}

// StorageVolumeProjectFromRecord returns the project name to use to for the volume based on the supplied project.
// For custom volume type, if the project supplied has the "features.storage.volumes" flag enabled then the
// project name is returned, otherwise the default project name is returned. For all other volume types the
// supplied project's name is returned.
func StorageVolumeProjectFromRecord(p *api.Project, volumeType int) string {
	// Non-custom volumes always use the project specified.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return p.Name
	}

	// Custom volumes only use the project specified if the project has the features.storage.volumes feature
	// enabled, otherwise the legacy behaviour of using the default project for custom volumes is used.
	if shared.IsTrue(p.Config["features.storage.volumes"]) {
		return p.Name
	}

	return Default
}
