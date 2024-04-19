package api

import (
	"net"
	"strings"
)

// NetworkLoadBalancerBackend represents a target backend specification in a network load balancer
//
// swagger:model
//
// API extension: network_load_balancer.
type NetworkLoadBalancerBackend struct {
	// lxdmeta:generate(entities=network-load-balancer; group=load-balancer-backend-properties; key=name)
	//
	// ---
	//  type: string
	//  required: yes
	//  shortdesc: Name of the backend

	// Name of the load balancer backend
	// Example: c1-http
	Name string `json:"name" yaml:"name"`

	// lxdmeta:generate(entities=network-load-balancer; group=load-balancer-backend-properties; key=description)
	//
	// ---
	//  type: string
	//  required: no
	//  shortdesc: Description of the backend

	// Description of the load balancer backend
	// Example: C1 webserver
	Description string `json:"description" yaml:"description"`

	// lxdmeta:generate(entities=network-load-balancer; group=load-balancer-backend-properties; key=target_port)
	// For example: `70,80-90` or `90`
	// ---
	//  type: string
	//  required: no
	//  defaultdesc: same as {config:option}`network-load-balancer-load-balancer-port-properties:listen_port`
	//  shortdesc: Target port or ports

	// TargetPort(s) to forward ListenPorts to (allows for many-to-one)
	// Example: 80,81,8080-8090
	TargetPort string `json:"target_port" yaml:"target_port"`

	// lxdmeta:generate(entities=network-load-balancer; group=load-balancer-backend-properties; key=target_address)
	//
	// ---
	//  type: string
	//  required: yes
	//  shortdesc: IP address to forward to

	// TargetAddress to forward ListenPorts to
	// Example: 198.51.100.2
	TargetAddress string `json:"target_address" yaml:"target_address"`
}

// Normalise normalises the fields in the load balancer backend so that they are comparable with ones stored.
func (b *NetworkLoadBalancerBackend) Normalise() {
	b.Description = strings.TrimSpace(b.Description)
	b.TargetAddress = strings.TrimSpace(b.TargetAddress)

	ip := net.ParseIP(b.TargetAddress)
	if ip != nil {
		b.TargetAddress = ip.String() // Replace with canonical form if specified.
	}

	// Remove space from TargetPort list.
	subjects := strings.Split(b.TargetPort, ",")
	for i, s := range subjects {
		subjects[i] = strings.TrimSpace(s)
	}

	b.TargetPort = strings.Join(subjects, ",")
}

// NetworkLoadBalancerPort represents a port specification in a network load balancer
//
// swagger:model
//
// API extension: network_load_balancer.
type NetworkLoadBalancerPort struct {
	// lxdmeta:generate(entities=network-load-balancer; group=load-balancer-port-properties; key=description)
	//
	// ---
	//  type: string
	//  required: no
	//  shortdesc: Description of the port or ports

	// Description of the load balancer port
	// Example: My web server load balancer
	Description string `json:"description" yaml:"description"`

	// lxdmeta:generate(entities=network-load-balancer; group=load-balancer-port-properties; key=protocol)
	// Possible values are `tcp` and `udp`.
	// ---
	//  type: string
	//  required: yes
	//  shortdesc: Protocol for the port or ports

	// Protocol for load balancer port (either tcp or udp)
	// Example: tcp
	Protocol string `json:"protocol" yaml:"protocol"`

	// lxdmeta:generate(entities=network-load-balancer; group=load-balancer-port-properties; key=listen_port)
	// For example: `80,90-100`
	// ---
	//  type: string
	//  required: yes
	//  shortdesc: Listen port or ports

	// ListenPort(s) of load balancer (comma delimited ranges)
	// Example: 80,81,8080-8090
	ListenPort string `json:"listen_port" yaml:"listen_port"`

	// lxdmeta:generate(entities=network-load-balancer; group=load-balancer-port-properties; key=target_backend)
	//
	// ---
	//  type: backend list
	//  required: yes
	//  shortdesc: Backend name or names to forward to

	// TargetBackend backend names to load balance ListenPorts to
	// Example: ["c1-http","c2-http"]
	TargetBackend []string `json:"target_backend" yaml:"target_backend"`
}

// Normalise normalises the fields in the load balancer port so that they are comparable with ones stored.
func (p *NetworkLoadBalancerPort) Normalise() {
	p.Description = strings.TrimSpace(p.Description)
	p.Protocol = strings.TrimSpace(p.Protocol)

	// Remove space from ListenPort list.
	subjects := strings.Split(p.ListenPort, ",")
	for i, s := range subjects {
		subjects[i] = strings.TrimSpace(s)
	}

	p.ListenPort = strings.Join(subjects, ",")
}

// NetworkLoadBalancersPost represents the fields of a new LXD network load balancer
//
// swagger:model
//
// API extension: network_load_balancer.
type NetworkLoadBalancersPost struct {
	NetworkLoadBalancerPut `yaml:",inline"`

	// lxdmeta:generate(entities=network-load-balancer; group=load-balancer-properties; key=listen_address)
	//
	// ---
	//  type: string
	//  required: no
	//  shortdesc: IP address to listen on

	// The listen address of the load balancer
	// Example: 192.0.2.1
	ListenAddress string `json:"listen_address" yaml:"listen_address"`
}

// NetworkLoadBalancerPut represents the modifiable fields of a LXD network load balancer
//
// swagger:model
//
// API extension: network_load_balancer.
type NetworkLoadBalancerPut struct {
	// lxdmeta:generate(entities=network-load-balancer; group=load-balancer-properties; key=description)
	//
	// ---
	//  type: string
	//  required: no
	//  shortdesc: Description of the network load balancer

	// Description of the load balancer listen IP
	// Example: My public IP load balancer
	Description string `json:"description" yaml:"description"`

	// lxdmeta:generate(entities=network-load-balancer; group=load-balancer-properties; key=config)
	// The only supported keys are `user.*` custom keys.
	// ---
	//  type: string set
	//  required: no
	//  shortdesc: User-provided free-form key/value pairs

	// Load balancer configuration map (refer to doc/network-load-balancers.md)
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`

	// lxdmeta:generate(entities=network-load-balancer; group=load-balancer-properties; key=backends)
	// See {ref}`network-load-balancers-backend-specifications`.
	// ---
	//  type: backend list
	//  required: no
	//  shortdesc: List of backend specifications

	// Backends (optional)
	Backends []NetworkLoadBalancerBackend `json:"backends" yaml:"backends"`

	// lxdmeta:generate(entities=network-load-balancer; group=load-balancer-properties; key=ports)
	// See {ref}`network-load-balancers-port-specifications`.
	// ---
	//  type: port list
	//  required: no
	//  shortdesc: List of port specifications

	// Port forwards (optional)
	Ports []NetworkLoadBalancerPort `json:"ports" yaml:"ports"`
}

// Normalise normalises the fields in the load balancer so that they are comparable with ones stored.
func (f *NetworkLoadBalancerPut) Normalise() {
	f.Description = strings.TrimSpace(f.Description)

	for i := range f.Backends {
		f.Backends[i].Normalise()
	}

	for i := range f.Ports {
		f.Ports[i].Normalise()
	}
}

// NetworkLoadBalancer used for displaying a network load balancer
//
// swagger:model
//
// API extension: network_load_balancer.
type NetworkLoadBalancer struct {
	// The listen address of the load balancer
	// Example: 192.0.2.1
	ListenAddress string `json:"listen_address" yaml:"listen_address"`

	// What cluster member this record was found on
	// Example: lxd01
	Location string `json:"location" yaml:"location"`

	// Description of the load balancer listen IP
	// Example: My public IP load balancer
	Description string `json:"description" yaml:"description"`

	// Load balancer configuration map (refer to doc/network-load-balancers.md)
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`

	// Backends (optional)
	Backends []NetworkLoadBalancerBackend `json:"backends" yaml:"backends"`

	// Port forwards (optional)
	Ports []NetworkLoadBalancerPort `json:"ports" yaml:"ports"`
}

// Normalise normalises the fields in the load balancer so that they are comparable with ones stored.
func (lb *NetworkLoadBalancer) Normalise() {
	lbPut := lb.Writable()
	lbPut.Normalise()
	lb.SetWritable(lbPut)
}

// Etag returns the values used for etag generation.
func (lb *NetworkLoadBalancer) Etag() []any {
	return []any{lb.ListenAddress, lb.Description, lb.Config, lb.Backends, lb.Ports}
}

// Writable converts a full NetworkLoadBalancer struct into a NetworkLoadBalancerPut struct (filters read-only fields).
func (lb *NetworkLoadBalancer) Writable() NetworkLoadBalancerPut {
	return NetworkLoadBalancerPut{
		Description: lb.Description,
		Config:      lb.Config,
		Backends:    lb.Backends,
		Ports:       lb.Ports,
	}
}

// SetWritable sets applicable values from NetworkLoadBalancerPut struct to NetworkLoadBalancer struct.
func (lb *NetworkLoadBalancer) SetWritable(put NetworkLoadBalancerPut) {
	lb.Description = put.Description
	lb.Config = put.Config
	lb.Backends = put.Backends
	lb.Ports = put.Ports
}
