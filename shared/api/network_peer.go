package api

// NetworkPeersPost represents the fields of a new LXD network peering
//
// swagger:model
//
// API extension: network_peer.
type NetworkPeersPost struct {
	NetworkPeerPut `yaml:",inline"`

	// lxdmeta:generate(entities=network-peering; group=peering-properties; key=name)
	//
	// ---
	//  type: string
	//  required: yes
	//  shortdesc: Name of the network peering on the local network

	// Name of the peer
	// Example: project1-network1
	Name string `json:"name" yaml:"name"`

	// lxdmeta:generate(entities=network-peering; group=peering-properties; key=target_project)
	// This option must be set at create time.
	// ---
	//  type: string
	//  required: yes
	//  shortdesc: Which project the target network exists in

	// Name of the target project
	// Example: project1
	TargetProject string `json:"target_project" yaml:"target_project"`

	// lxdmeta:generate(entities=network-peering; group=peering-properties; key=target_network)
	// This option must be set at create time.
	// ---
	//  type: string
	//  required: yes
	//  shortdesc: Which network to create a peering with

	// Name of the target network
	// Example: network1
	TargetNetwork string `json:"target_network" yaml:"target_network"`
}

// NetworkPeerPut represents the modifiable fields of a LXD network peering
//
// swagger:model
//
// API extension: network_peer.
type NetworkPeerPut struct {
	// lxdmeta:generate(entities=network-peering; group=peering-properties; key=description)
	//
	// ---
	//  type: string
	//  required: no
	//  shortdesc: Description of the network peering

	// Description of the peer
	// Example: Peering with network1 in project1
	Description string `json:"description" yaml:"description"`

	// lxdmeta:generate(entities=network-peering; group=peering-properties; key=config)
	// The only supported keys are `user.*` custom keys.
	// ---
	//  type: string set
	//  required: no
	//  shortdesc: User-provided free-form key/value pairs

	// Peer configuration map (refer to doc/network-peers.md)
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`
}

// NetworkPeer used for displaying a LXD network peering.
//
// swagger:model
//
// API extension: network_forward.
type NetworkPeer struct {
	// Name of the peer
	// Read only: true
	// Example: project1-network1
	Name string `json:"name" yaml:"name"`

	// Description of the peer
	// Example: Peering with network1 in project1
	Description string `json:"description" yaml:"description"`

	// Name of the target project
	// Read only: true
	// Example: project1
	TargetProject string `json:"target_project" yaml:"target_project"`

	// Name of the target network
	// Read only: true
	// Example: network1
	TargetNetwork string `json:"target_network" yaml:"target_network"`

	// lxdmeta:generate(entities=network-peering; group=peering-properties; key=status)
	// Indicates if mutual peering exists with the target network.
	// This property is read-only and cannot be updated.
	// ---
	//  type: string
	//  required: --
	//  shortdesc: Status indicating if pending or created

	// The state of the peering
	// Read only: true
	// Example: Pending
	Status string `json:"status" yaml:"status"`

	// Peer configuration map (refer to doc/network-peers.md)
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`

	// List of URLs of objects using this network peering
	// Read only: true
	// Example: ["/1.0/network-acls/test", "/1.0/network-acls/foo"]
	UsedBy []string `json:"used_by" yaml:"used_by"`
}

// Etag returns the values used for etag generation.
func (p *NetworkPeer) Etag() []any {
	return []any{p.Name, p.Description, p.Config}
}

// Writable converts a full NetworkPeer struct into a NetworkPeerPut struct (filters read-only fields).
func (p *NetworkPeer) Writable() NetworkPeerPut {
	return NetworkPeerPut{
		Description: p.Description,
		Config:      p.Config,
	}
}

// SetWritable sets applicable values from NetworkPeerPut struct to NetworkPeer struct.
func (p *NetworkPeer) SetWritable(put NetworkPeerPut) {
	p.Description = put.Description
	p.Config = put.Config
}
