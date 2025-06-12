package api

import (
	"encoding/json"
)

// DevLXDResponse represents the response from the devLXD API.
type DevLXDResponse struct {
	Content    []byte `json:"content" yaml:"content"`
	StatusCode int    `json:"status_code" yaml:"status_code"`
}

// ContentAsStruct unmarshals the response content.
func (r DevLXDResponse) ContentAsStruct(target any) error {
	return json.Unmarshal(r.Content, &target)
}

// DevLXDPut represents the modifiable data.
type DevLXDPut struct {
	// Instance state
	// Example: Started
	State string `json:"state" yaml:"state"`
}

// DevLXDGet represents the server data which is returned as the root of the devlxd API.
type DevLXDGet struct {
	DevLXDPut

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
