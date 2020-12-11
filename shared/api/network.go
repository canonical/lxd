package api

// NetworksPost represents the fields of a new LXD network
//
// API extension: network
type NetworksPost struct {
	NetworkPut `yaml:",inline"`

	Name string `json:"name" yaml:"name"`
	Type string `json:"type" yaml:"type"`
}

// NetworkPost represents the fields required to rename a LXD network
//
// API extension: network
type NetworkPost struct {
	Name string `json:"name" yaml:"name"`
}

// NetworkPut represents the modifiable fields of a LXD network
//
// API extension: network
type NetworkPut struct {
	Config map[string]string `json:"config" yaml:"config"`

	// API extension: entity_description
	Description string `json:"description" yaml:"description"`
}

// NetworkStatusPending network is pending creation on other cluster nodes.
const NetworkStatusPending = "Pending"

// NetworkStatusCreated network is fully created.
const NetworkStatusCreated = "Created"

// NetworkStatusErrored network is in error status.
const NetworkStatusErrored = "Errored"

// NetworkStatusUnknown network is in unknown status.
const NetworkStatusUnknown = "Unknown"

// Network represents a LXD network
type Network struct {
	NetworkPut `yaml:",inline"`

	Name   string   `json:"name" yaml:"name"`
	Type   string   `json:"type" yaml:"type"`
	UsedBy []string `json:"used_by" yaml:"used_by"`

	// API extension: network
	Managed bool `json:"managed" yaml:"managed"`

	// API extension: clustering
	Status    string   `json:"status" yaml:"status"`
	Locations []string `json:"locations" yaml:"locations"`
}

// Writable converts a full Network struct into a NetworkPut struct (filters read-only fields)
func (network *Network) Writable() NetworkPut {
	return network.NetworkPut
}

// NetworkLease represents a DHCP lease
//
// API extension: network_leases
type NetworkLease struct {
	Hostname string `json:"hostname" yaml:"hostname"`
	Hwaddr   string `json:"hwaddr" yaml:"hwaddr"`
	Address  string `json:"address" yaml:"address"`
	Type     string `json:"type" yaml:"type"`

	// API extension: network_leases_location
	Location string `json:"location" yaml:"location"`
}

// NetworkState represents the network state
type NetworkState struct {
	Addresses []NetworkStateAddress `json:"addresses" yaml:"addresses"`
	Counters  NetworkStateCounters  `json:"counters" yaml:"counters"`
	Hwaddr    string                `json:"hwaddr" yaml:"hwaddr"`
	Mtu       int                   `json:"mtu" yaml:"mtu"`
	State     string                `json:"state" yaml:"state"`
	Type      string                `json:"type" yaml:"type"`

	// API extension: network_state_bond_bridge
	Bond   *NetworkStateBond   `json:"bond" yaml:"bond"`
	Bridge *NetworkStateBridge `json:"bridge" yaml:"bridge"`

	// API extension: network_state_vlan
	VLAN *NetworkStateVLAN `json:"vlan" yaml:"vlan"`
}

// NetworkStateAddress represents a network address
type NetworkStateAddress struct {
	Family  string `json:"family" yaml:"family"`
	Address string `json:"address" yaml:"address"`
	Netmask string `json:"netmask" yaml:"netmask"`
	Scope   string `json:"scope" yaml:"scope"`
}

// NetworkStateCounters represents packet counters
type NetworkStateCounters struct {
	BytesReceived   int64 `json:"bytes_received" yaml:"bytes_received"`
	BytesSent       int64 `json:"bytes_sent" yaml:"bytes_sent"`
	PacketsReceived int64 `json:"packets_received" yaml:"packets_received"`
	PacketsSent     int64 `json:"packets_sent" yaml:"packets_sent"`
}

// NetworkStateBond represents bond specific state
// API extension: network_state_bond_bridge
type NetworkStateBond struct {
	Mode           string `json:"mode" yaml:"mode"`
	TransmitPolicy string `json:"transmit_policy" yaml:"transmit_policy"`
	UpDelay        uint64 `json:"up_delay" yaml:"up_delay"`
	DownDelay      uint64 `json:"down_delay" yaml:"down_delay"`

	MIIFrequency uint64 `json:"mii_frequency" yaml:"mii_frequency"`
	MIIState     string `json:"mii_state" yaml:"mii_state"`

	LowerDevices []string `json:"lower_devices" yaml:"lower_devices"`
}

// NetworkStateBridge represents bridge specific state
// API extension: network_state_bond_bridge
type NetworkStateBridge struct {
	ID           string `json:"id" yaml:"id"`
	STP          bool   `json:"stp" yaml:"stp"`
	ForwardDelay uint64 `json:"forward_delay" yaml:"forward_delay"`

	VLANDefault   uint64 `json:"vlan_default" yaml:"vlan_default"`
	VLANFiltering bool   `json:"vlan_filtering" yaml:"vlan_filtering"`

	UpperDevices []string `json:"upper_devices" yaml:"upper_devices"`
}

// NetworkStateVLAN represents VLAN specific state
// API extension: network_state_vlan
type NetworkStateVLAN struct {
	LowerDevice string `json:"lower_device" yaml:"lower_device"`
	VID         uint64 `json:"vid" yaml:"vid"`
}
