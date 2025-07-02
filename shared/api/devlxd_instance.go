package api

// DevLXDInstance is a devLXD representation of LXD instance.
type DevLXDInstance struct {
	// Instance name
	// Example: foo
	Name string `json:"name" yaml:"name"`

	// Instance devices
	// Example: {"root": {"type": "disk", "pool": "default", "path": "/"}}
	Devices map[string]map[string]string `json:"devices" yaml:"devices"`
}

// DevLXDInstancePut represents the modifiable fields of LXD instance
// that can be updated via the devLXD API.
type DevLXDInstancePut struct {
	// Instance devices
	// Example: {"root": {"type": "disk", "pool": "default", "path": "/"}}
	Devices map[string]map[string]string `json:"devices" yaml:"devices"`
}
