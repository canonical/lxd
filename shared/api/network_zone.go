package api

// NetworkZonesPost represents the fields of a new LXD network zone
//
// swagger:model
//
// API extension: network_dns
type NetworkZonesPost struct {
	NetworkZonePut `yaml:",inline"`

	// The name of the zone (DNS domain name)
	// Example: example.net
	Name string `json:"name" yaml:"name"`
}

// NetworkZonePut represents the modifiable fields of a LXD network zoned
//
// swagger:model
//
// API extension: network_dns
type NetworkZonePut struct {
	// Description of the network zone
	// Example: Internal domain
	Description string `json:"description" yaml:"description"`

	// Zone configuration map (refer to doc/network-zones.md)
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`
}

// NetworkZone represents a network zone (DNS).
//
// swagger:model
//
// API extension: network_dns
type NetworkZone struct {
	NetworkZonePut `yaml:",inline"`

	// The name of the zone (DNS domain name)
	// Example: example.net
	Name string `json:"name" yaml:"name"`

	// List of URLs of objects using this network zone
	// Read only: true
	// Example: ["/1.0/networks/foo", "/1.0/networks/bar"]
	UsedBy []string `json:"used_by" yaml:"used_by"` // Resources that use the zone.
}

// Writable converts a full NetworkZone struct into a NetworkZonePut struct (filters read-only fields).
func (f *NetworkZone) Writable() NetworkZonePut {
	return f.NetworkZonePut
}
