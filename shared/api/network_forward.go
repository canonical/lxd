package api

import (
	"net"
	"strings"
)

// NetworkForwardPort represents a port specification in a network address forward
//
// swagger:model
//
// API extension: network_forward.
type NetworkForwardPort struct {
	// lxdmeta:generate(entities=network-forward; group=port-properties; key=description)
	//
	// ---
	//  type: string
	//  required: no
	//  shortdesc: Description of the port or ports

	// Description of the forward port
	// Example: My web server forward
	Description string `json:"description" yaml:"description"`

	// lxdmeta:generate(entities=network-forward; group=port-properties; key=protocol)
	//  Possible values are `tcp` and `udp`.
	// ---
	//  type: string
	//  required: yes
	//  shortdesc: Protocol for the port or ports

	// Protocol for port forward (either tcp or udp)
	// Example: tcp
	Protocol string `json:"protocol" yaml:"protocol"`

	// lxdmeta:generate(entities=network-forward; group=port-properties; key=listen_port)
	// For example: `80,90-100`
	// ---
	//  type: string
	//  required: yes
	//  shortdesc: Listen port or ports

	// ListenPort(s) to forward (comma delimited ranges)
	// Example: 80,81,8080-8090
	ListenPort string `json:"listen_port" yaml:"listen_port"`

	// lxdmeta:generate(entities=network-forward; group=port-properties; key=target_port)
	// For example: `70,80-90` or `90`
	// ---
	//  type: string
	//  required: no
	//  defaultdesc: same as `listen_port`
	//  shortdesc: Target port or ports

	// TargetPort(s) to forward ListenPorts to (allows for many-to-one)
	// Example: 80,81,8080-8090
	TargetPort string `json:"target_port" yaml:"target_port"`

	// lxdmeta:generate(entities=network-forward; group=port-properties; key=target_address)
	//
	// ---
	//  type: string
	//  required: yes
	//  shortdesc: IP address to forward to

	// TargetAddress to forward ListenPorts to
	// Example: 198.51.100.2
	TargetAddress string `json:"target_address" yaml:"target_address"`
}

// Normalise normalises the fields in the rule so that they are comparable with ones stored.
func (p *NetworkForwardPort) Normalise() {
	p.Description = strings.TrimSpace(p.Description)
	p.Protocol = strings.TrimSpace(p.Protocol)
	p.TargetAddress = strings.TrimSpace(p.TargetAddress)

	ip := net.ParseIP(p.TargetAddress)
	if ip != nil {
		p.TargetAddress = ip.String() // Replace with canonical form if specified.
	}

	// Remove space from ListenPort list.
	subjects := strings.Split(p.ListenPort, ",")
	for i, s := range subjects {
		subjects[i] = strings.TrimSpace(s)
	}

	p.ListenPort = strings.Join(subjects, ",")

	// Remove space from TargetPort list.
	subjects = strings.Split(p.TargetPort, ",")
	for i, s := range subjects {
		subjects[i] = strings.TrimSpace(s)
	}

	p.TargetPort = strings.Join(subjects, ",")
}

// NetworkForwardsPost represents the fields of a new LXD network address forward
//
// swagger:model
//
// API extension: network_forward.
type NetworkForwardsPost struct {
	NetworkForwardPut `yaml:",inline"`

	// lxdmeta:generate(entities=network-forward; group=forward-properties; key=listen_address)
	//
	// ---
	//  type: string
	//  required: no
	//  shortdesc: IP address to listen on

	// The listen address of the forward
	// Example: 192.0.2.1
	ListenAddress string `json:"listen_address" yaml:"listen_address"`
}

// Normalise normalises the fields in the rule so that they are comparable with ones stored.
func (f *NetworkForwardsPost) Normalise() {
	ip := net.ParseIP(f.ListenAddress)
	if ip != nil {
		f.ListenAddress = ip.String() // Replace with canonical form if specified.
	}

	f.NetworkForwardPut.Normalise()
}

// NetworkForwardPut represents the modifiable fields of a LXD network address forward
//
// swagger:model
//
// API extension: network_forward.
type NetworkForwardPut struct {
	// lxdmeta:generate(entities=network-forward; group=forward-properties; key=description)
	//
	// ---
	//  type: string
	//  required: yes
	//  shortdesc: Description of the network forward

	// Description of the forward listen IP
	// Example: My public IP forward
	Description string `json:"description" yaml:"description"`

	// lxdmeta:generate(entities=network-forward; group=forward-properties; key=config)
	// The only supported keys are `target_address` and `user.*` custom keys.
	// ---
	//  type: string set
	//  required: no
	//  shortdesc: User-provided free-form key/value pairs

	// Forward configuration map (refer to doc/network-forwards.md)
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`

	// lxdmeta:generate(entities=network-forward; group=forward-properties; key=ports)
	// See {ref}`network-forwards-port-specifications`.
	// ---
	//  type: port list
	//  required: no
	//  shortdesc: List of port specifications

	// Port forwards (optional)
	Ports []NetworkForwardPort `json:"ports" yaml:"ports"`
}

// Normalise normalises the fields in the rule so that they are comparable with ones stored.
func (f *NetworkForwardPut) Normalise() {
	f.Description = strings.TrimSpace(f.Description)

	ip := net.ParseIP(f.Config["target_address"])
	if ip != nil {
		f.Config["target_address"] = ip.String() // Replace with canonical form if specified.
	}

	for i := range f.Ports {
		f.Ports[i].Normalise()
	}
}

// NetworkForward used for displaying an network address forward.
//
// swagger:model
//
// API extension: network_forward.
type NetworkForward struct {
	// The listen address of the forward
	// Example: 192.0.2.1
	ListenAddress string `json:"listen_address" yaml:"listen_address"`

	// What cluster member this record was found on
	// Example: lxd01
	Location string `json:"location" yaml:"location"`

	// Description of the forward listen IP
	// Example: My public IP forward
	Description string `json:"description" yaml:"description"`

	// Forward configuration map (refer to doc/network-forwards.md)
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`

	// Port forwards (optional)
	Ports []NetworkForwardPort `json:"ports" yaml:"ports"`
}

// Normalise normalises the fields in the rule so that they are comparable with ones stored.
func (f *NetworkForward) Normalise() {
	fPut := f.Writable()
	fPut.Normalise()
	f.SetWritable(fPut)
}

// Etag returns the values used for etag generation.
func (f *NetworkForward) Etag() []any {
	return []any{f.ListenAddress, f.Description, f.Config, f.Ports}
}

// Writable converts a full NetworkForward struct into a NetworkForwardPut struct (filters read-only fields).
func (f *NetworkForward) Writable() NetworkForwardPut {
	return NetworkForwardPut{
		Description: f.Description,
		Config:      f.Config,
		Ports:       f.Ports,
	}
}

// SetWritable sets applicable values from NetworkForwardPut struct to NetworkForward struct.
func (f *NetworkForward) SetWritable(put NetworkForwardPut) {
	f.Description = put.Description
	f.Config = put.Config
	f.Ports = put.Ports
}
