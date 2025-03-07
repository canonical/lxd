package api

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

// UbuntuProGuestTokenResponse contains the expected fields of proAPIGetGuestTokenV1 that must be passed back to
// the guest for pro attachment to succeed.
//
// API extension: ubuntu_pro_guest_attach.
type UbuntuProGuestTokenResponse struct {
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

// UbuntuProSettings contains Ubuntu Pro settings relevant to LXD.
//
// API extension: ubuntu_pro_guest_attach.
type UbuntuProSettings struct {
	// GuestAttach indicates the availability of ubuntu pro guest attachment.
	//
	// Example: on
	GuestAttach string `json:"guest_attach"`
}
