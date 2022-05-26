package cluster

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t instance_profiles.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e instance_profile objects-by-ProfileID version=2
//
//go:generate mapper method -i -e instance_profile GetMany struct=Profile version=2

// InstanceProfile is an association table struct that associates Instances
// to Profiles.
type InstanceProfile struct {
	InstanceID int `db:"primary=yes&order=yes"`
	ProfileID  int
	ApplyOrder int `db:"order=yes"`
}

// InstanceProfileFilter specifies potential query parameter fields.
type InstanceProfileFilter struct {
	InstanceID *int
	ProfileID  *int
}
