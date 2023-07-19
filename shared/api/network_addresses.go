package api

// NetworkAllocations used for displaying network addresses used by a consuming entity
// e.g, instance, network forward, load-balancer, network...
//
// swagger:model
//
// API extension: network_allocations.
type NetworkAllocations struct {
	// The CIDR subnet of the forward
	// Example: 192.0.2.1/24
	Addresses []string `json:"addresses" yaml:"addresses"`
	// Name of the entity consuming the network address
	UsedBy string `json:"used_by" yaml:"used_by"`
	// Type of the entity consuming the network address
	Type string `json:"type" yaml:"type"`
	// Whether the entity comes from a network that LXD performs NAT on from those that are directly routed from the external network
	NAT bool `json:"nat" yaml:"nat"`
	// Hwaddr is the MAC address of the entity consuming the network address
	Hwaddr string `json:"hwaddr" yaml:"hwaddr"`
}
