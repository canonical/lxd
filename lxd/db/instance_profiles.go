//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

import "fmt"

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t instance_profiles.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -p db -e instance_profile objects
//go:generate mapper stmt -p db -e instance_profile create struct=InstanceProfile
//go:generate mapper stmt -p db -e instance_profile delete-by-InstanceID
//
//go:generate mapper method -p db -e instance_profile GetMany struct=Profile
//go:generate mapper method -p db -e instance_profile GetMany struct=Instance
//go:generate mapper method -p db -e instance_profile Create struct=Instance
//go:generate mapper method -p db -e instance_profile DeleteMany struct=Instance

// InstanceProfile is an association table struct that associates Instances
// to Profiles.
type InstanceProfile struct {
	InstanceID int `db:"primary=yes"`
	ProfileID  int
	ApplyOrder int
}

// InstanceProfileFilter specifies potential query parameter fields.
type InstanceProfileFilter struct {
	InstanceID *int
	ProfileID  *int
}

// UpdateInstanceProfiles updates the profiles of an instance in the order they are given.
func (c *ClusterTx) UpdateInstanceProfiles(instance Instance) error {
	err := c.DeleteInstanceProfiles(instance)
	if err != nil {
		return err
	}

	project := instance.Project
	enabled, err := projectHasProfiles(c.tx, project)
	if err != nil {
		return fmt.Errorf("Check if project has profiles: %w", err)
	}

	if !enabled {
		project = "default"
	}

	applyOrder := 1
	stmt := c.stmt(instanceProfileCreate)

	for _, name := range instance.Profiles {
		var instanceID int64
		var profileID int64

		profileID, err = c.GetProfileID(project, name)
		instanceID = int64(instance.ID)

		if err != nil {
			return err
		}

		_, err = stmt.Exec(instanceID, profileID, applyOrder)
		if err != nil {
		}
		applyOrder = applyOrder + 1
	}

	return nil
}
