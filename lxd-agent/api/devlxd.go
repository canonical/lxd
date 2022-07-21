package api

// DevLXDGet represents the server data which is returned as the root of the devlxd API.
type DevLXDGet struct {
	// API version number
	// Example: 1.0
	APIVersion string `json:"api_version" yaml:"api_version"`

	// Type (container or virtual-machine)
	// Example: container
	InstanceType string `json:"instance_type" yaml:"instance_type"`

	// What cluster member this instance is located on
	// Example: lxd01
	Location string `json:"location" yaml:"location"`
}
