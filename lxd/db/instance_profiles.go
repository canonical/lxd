//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

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
