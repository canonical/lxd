package api

// ProfilesPost represents the fields of a new LXD profile
type ProfilesPost struct {
	ProfilePut `yaml:",inline"`

	Name string `json:"name"`
}

// ProfilePost represents the fields required to rename a LXD profile
type ProfilePost struct {
	Name string `json:"name"`
}

// ProfilePut represents the modifiable fields of a LXD profile
type ProfilePut struct {
	Config      map[string]string            `json:"config"`
	Description string                       `json:"description"`
	Devices     map[string]map[string]string `json:"devices"`
}

// Profile represents a LXD profile
type Profile struct {
	ProfilePut `yaml:",inline"`

	Name string `json:"name"`

	// API extension: profile_usedby
	UsedBy []string `json:"used_by"`
}

// Writable converts a full Profile struct into a ProfilePut struct (filters read-only fields)
func (profile *Profile) Writable() ProfilePut {
	return profile.ProfilePut
}
