//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t instance_profiles.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -p db -e instance_profile objects-by-ProfileID
//go:generate mapper stmt -p db -e instance_profile objects-by-InstanceID
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
func (c *ClusterTx) UpdateInstanceProfiles(instance Instance, profiles []Profile) error {
	err := c.DeleteInstanceProfiles(instance)
	if err != nil {
		return err
	}

	applyOrder := 1
	stmt := c.stmt(instanceProfileCreate)
	for _, profile := range profiles {
		_, err := stmt.Exec(instance.ID, profile.ID, applyOrder)
		if err != nil {
			return err
		}

		applyOrder = applyOrder + 1
	}

	return nil
}
