package api

// ProfilesPost represents the fields of a new LXD profile
//
// swagger:model
type ProfilesPost struct {
	ProfilePut `yaml:",inline"`

	// The name of the new profile
	// Example: foo
	Name string `db:"primary=yes" json:"name" yaml:"name"`
}

// ProfilePost represents the fields required to rename a LXD profile
//
// swagger:model
type ProfilePost struct {
	// The new name for the profile
	// Example: bar
	Name string `json:"name" yaml:"name"`
}

// ProfilePut represents the modifiable fields of a LXD profile
//
// swagger:model
type ProfilePut struct {
	// Instance configuration map (refer to doc/instances.md)
	// Example: {"limits.cpu": "4", "limits.memory": "4GiB"}
	Config map[string]string `json:"config" yaml:"config"`

	// Description of the profile
	// Example: Medium size instances
	Description string `json:"description" yaml:"description"`

	// List of devices
	// Example: {"root": {"type": "disk", "pool": "default", "path": "/"}, "eth0": {"type": "nic", "network": "lxdbr0", "name": "eth0"}}
	Devices map[string]map[string]string `json:"devices" yaml:"devices"`
}

// Profile represents a LXD profile
//
// swagger:model
type Profile struct {
	WithEntitlements `yaml:",inline"` //nolint:musttag

	// The profile name
	// Read only: true
	// Example: foo
	Name string `db:"primary=yes" json:"name" yaml:"name"`

	// Description of the profile
	// Example: Medium size instances
	Description string `json:"description" yaml:"description"`

	// Instance configuration map (refer to doc/instances.md)
	// Example: {"limits.cpu": "4", "limits.memory": "4GiB"}
	Config map[string]string `json:"config" yaml:"config"`

	// List of devices
	// Example: {"root": {"type": "disk", "pool": "default", "path": "/"}, "eth0": {"type": "nic", "network": "lxdbr0", "name": "eth0"}}
	Devices map[string]map[string]string `json:"devices" yaml:"devices"`

	// List of URLs of objects using this profile
	// Read only: true
	// Example: ["/1.0/instances/c1", "/1.0/instances/v1"]
	//
	// API extension: profile_usedby
	UsedBy []string `json:"used_by" yaml:"used_by"`

	// Project name
	// Example: project1
	//
	// API extension: profiles_all_projects
	Project string `json:"project" yaml:"project"`
}

// Writable converts a full Profile struct into a ProfilePut struct (filters read-only fields).
func (profile *Profile) Writable() ProfilePut {
	return ProfilePut{
		Description: profile.Description,
		Config:      profile.Config,
		Devices:     profile.Devices,
	}
}

// SetWritable sets applicable values from ProfilePut struct to Profile struct.
func (profile *Profile) SetWritable(put ProfilePut) {
	profile.Description = put.Description
	profile.Config = put.Config
	profile.Devices = put.Devices
}

// URL returns the URL for the profile.
func (profile *Profile) URL(apiVersion string, projectName string) *URL {
	return NewURL().Path(apiVersion, "profiles", profile.Name).Project(projectName)
}
