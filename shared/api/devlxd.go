package api

import (
	"encoding/json"
)

// DevLXDResponse represents the response from the devLXD API.
type DevLXDResponse struct {
	Content    json.RawMessage `json:"content" yaml:"content"`
	StatusCode int             `json:"status_code" yaml:"status_code"`
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

// DevLXDGetUntrusted represents the server data which is returned as the root of the devlxd API.
type DevLXDGetUntrusted struct {
	DevLXDPut `yaml:",inline"`

	// API version number
	// Example: 1.0
	APIVersion string `json:"api_version" yaml:"api_version"`

	// Type (container or virtual-machine)
	// Example: container
	InstanceType string `json:"instance_type" yaml:"instance_type"`

	// What cluster member this instance is located on
	// Example: lxd01
	Location string `json:"location" yaml:"location"`

	// Whether the caller is trusted or untrusted
	// Example: untrusted
	Auth string `json:"auth" yaml:"auth"`

	// List of supported storage drivers.
	// Example: [{"name": "zfs", "remote": false}, {"name": "ceph", "remote": true}]
	// API extension: devlxd_volume_management
	SupportedStorageDrivers []DevLXDServerStorageDriverInfo `json:"supported_storage_drivers" yaml:"supported_storage_drivers"`
}

// DevLXDGet represents the server data which is returned as the root of the devlxd API for a trusted client.
type DevLXDGet struct {
	DevLXDGetUntrusted `yaml:",inline"`

	// Read-only information about DevLXD server environment.
	// API extension: devlxd_volume_management
	Environment DevLXDServerEnvironment `json:"environment" yaml:"environment"`
}

// DevLXDServerStorageDriverInfo represents the read-only info about a storage driver.
type DevLXDServerStorageDriverInfo struct {
	// Name of the driver.
	// Example: zfs
	Name string `json:"name" yaml:"name"`

	// Whether the driver has remote volumes.
	// Example: false
	Remote bool `json:"remote" yaml:"remote"`
}

// DevLXDServerEnvironment represents the read-only environment fields of a DevLXD server.
type DevLXDServerEnvironment struct {
	// Whether the underlying LXD server is part of a cluster.
	// This information is populated only for trusted clients.
	// Example: false
	ServerClustered bool `json:"server_clustered" yaml:"server_clustered"`
}

// DevLXDUbuntuProGuestTokenResponse contains the expected fields of proAPIGetGuestTokenV1 that must be passed back to
// the guest for pro attachment to succeed.
//
// API extension: ubuntu_pro_guest_attach.
type DevLXDUbuntuProGuestTokenResponse struct {
	// Expires denotes the time at which the token will expire.
	//
	// Example: 2025-03-23T20:00:00-04:00
	Expires string `json:"expires"`

	// GuestToken contains the guest Pro attach token.
	//
	// Example: RANDOM-STRING
	GuestToken string `json:"guest_token"`

	// ID is an identifier for the token.
	//
	// Example: 9f65c3d0-c326-491e-927f-9b062b6649a0
	ID string `json:"id"`
}

// DevLXDUbuntuProSettings contains Ubuntu Pro settings relevant to LXD.
//
// API extension: ubuntu_pro_guest_attach.
type DevLXDUbuntuProSettings struct {
	// GuestAttach indicates the availability of ubuntu pro guest attachment.
	//
	// Example: on
	GuestAttach string `json:"guest_attach"`
}
