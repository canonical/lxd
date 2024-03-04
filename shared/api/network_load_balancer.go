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
	// Name of the load balancer backend
	// Example: c1-http
	Name string `json:"name" yaml:"name"`

	// Description of the load balancer backend
	// Example: C1 webserver
	Description string `json:"description" yaml:"description"`

	// TargetPort(s) to forward ListenPorts to (allows for many-to-one)
	// Example: 80,81,8080-8090
	TargetPort string `json:"target_port" yaml:"target_port"`

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
	// Description of the load balancer port
	// Example: My web server load balancer
	Description string `json:"description" yaml:"description"`

	// Protocol for load balancer port (either tcp or udp)
	// Example: tcp
	Protocol string `json:"protocol" yaml:"protocol"`

	// ListenPort(s) of load balancer (comma delimited ranges)
	// Example: 80,81,8080-8090
	ListenPort string `json:"listen_port" yaml:"listen_port"`

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
