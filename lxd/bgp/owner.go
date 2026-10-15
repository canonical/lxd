package bgp

import (
	"strconv"
	"strings"
)

// Owner identifiers scope BGP prefixes and peers to a network and, optionally, a
// sub-owner such as a network forward, load balancer or instance NIC. Sub-owners
// are always prefixed by their parent network owner (e.g. "network_1"), which is
// what allows the export policy to match a peer's own routes plus all of its
// related sub-owner routes.
//
// An OVN network carries no BGP peers of its own: its peers are configured on the
// uplink network it is attached to. A peer is exported only the routes whose
// owner is the peer's own network or one of its sub-owners, so an OVN network's
// routes are nested under the owner of the uplink they are advertised to
// (network_<uplinkID>_ovn_<id>). This mirrors an OVN instance NIC's external
// routes, which are likewise registered under the uplink (see
// OwnerInstanceNetwork).
const (
	ownerNetworkPrefix      = "network"
	ownerOVNSuffix          = "ovn"
	ownerForwardSuffix      = "forward"
	ownerLoadBalancerSuffix = "load_balancer"
	ownerInstancePrefix     = "instance"
	ownerSeparator          = "_"
)

// OwnerNetwork returns the base owner identifier for a bridge or physical
// network's own prefixes (network_<id>).
func OwnerNetwork(networkID int64) string {
	return ownerNetworkPrefix + ownerSeparator + strconv.FormatInt(networkID, 10)
}

// OwnerNetworkOVN returns the base owner identifier for an OVN network
// (network_<uplinkID>_ovn_<id>). The OVN network's owner is nested under the
// uplink network it is attached to, so that the routes it owns are exported to
// the BGP peers configured on that uplink.
func OwnerNetworkOVN(uplinkNetworkID int64, networkID int64) string {
	return OwnerNetwork(uplinkNetworkID) + ownerSeparator + ownerOVNSuffix + ownerSeparator + strconv.FormatInt(networkID, 10)
}

// OwnerNetworkForwards returns the owner identifier for all of a network's
// address forwards (<base>_forward).
//
// A forward is not owned individually: its prefixes are cleared and re-added
// wholesale whenever the network's forwards are refreshed, so a per-forward
// owner would never be used to scope a removal.
func OwnerNetworkForwards(base string) string {
	return base + ownerSeparator + ownerForwardSuffix
}

// OwnerNetworkLoadBalancers returns the owner identifier for all of a network's
// load balancers (<base>_load_balancer).
//
// A load balancer is not owned individually: its prefixes are cleared and
// re-added wholesale whenever the network's load balancers are refreshed, so a
// per-load-balancer owner would never be used to scope a removal.
func OwnerNetworkLoadBalancers(base string) string {
	return base + ownerSeparator + ownerLoadBalancerSuffix
}

// OwnerInstanceNetwork returns the owner identifier for an instance NIC attached
// to a network, given the base owner of that network
// (<base>_instance_<instID>_<devName>).
func OwnerInstanceNetwork(base string, instanceID int, deviceName string) string {
	return base + ownerSeparator + ownerInstancePrefix + ownerSeparator + strconv.Itoa(instanceID) + ownerSeparator + deviceName
}

// ownerMatches reports whether pathOwner belongs to peerOwner, either exactly or
// as one of its sub-owners (e.g. peerOwner "network_1" matches "network_1",
// "network_1_forward", "network_1_load_balancer",
// "network_1_instance_<instID>_<devName>" and "network_1_ovn_<id>").
func ownerMatches(peerOwner string, pathOwner string) bool {
	return pathOwner == peerOwner || strings.HasPrefix(pathOwner, peerOwner+ownerSeparator)
}
