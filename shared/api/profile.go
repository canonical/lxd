package api

// ProfilesPost represents the fields of a new LXD profile
//
// swagger:model
type ProfilesPost struct {
	ProfilePut `yaml:",inline"`

	// The name of the new profile
	// Example: foo
	Name string `json:"name" yaml:"name" db:"primary=yes"`
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
	ProfilePut `yaml:",inline"`

	// The profile name
	// Read only: true
	// Example: foo
	Name string `json:"name" yaml:"name" db:"primary=yes"`

	// List of URLs of objects using this profile
	// Read only: true
	// Example: ["/1.0/instances/c1", "/1.0/instances/v1"]
	//
	// API extension: profile_usedby
	UsedBy []string `json:"used_by" yaml:"used_by"`
}

// Writable converts a full Profile struct into a ProfilePut struct (filters read-only fields)
func (profile *Profile) Writable() ProfilePut {
	return profile.ProfilePut
}

// URL returns the URL for the profile.
func (profile *Profile) URL(apiVersion string, project string) *URL {
	return NewURL().Path(apiVersion, "profiles", profile.Name).Project(project)
}
