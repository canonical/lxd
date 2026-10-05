package bgp

import (
	"fmt"
	"strings"
)

// Owner identifiers scope BGP prefixes and peers to a network and, optionally, a
// sub-owner such as a network forward, load balancer or instance NIC. Sub-owners
// are always prefixed by their parent network owner (e.g. "network_1"), which is
// what allows the export policy to match a peer's own routes plus all of its
// related sub-owner routes.
const (
	ownerNetworkPrefix      = "network"
	ownerForwardSuffix      = "forward"
	ownerLoadBalancerSuffix = "load_balancer"
	ownerInstancePrefix     = "instance"
	ownerSeparator          = "_"
)

// OwnerNetwork returns the owner identifier for a network (network_<id>).
func OwnerNetwork(networkID int64) string {
	return fmt.Sprintf("%s_%d", ownerNetworkPrefix, networkID)
}

// OwnerNetworkForward returns the owner identifier for a network forward
// (network_<id>_forward).
func OwnerNetworkForward(networkID int64) string {
	return fmt.Sprintf("%s_%d_%s", ownerNetworkPrefix, networkID, ownerForwardSuffix)
}

// OwnerNetworkLoadBalancer returns the owner identifier for a network load
// balancer (network_<id>_load_balancer).
func OwnerNetworkLoadBalancer(networkID int64) string {
	return fmt.Sprintf("%s_%d_%s", ownerNetworkPrefix, networkID, ownerLoadBalancerSuffix)
}

// OwnerInstanceNetwork returns the owner identifier for an instance NIC attached
// to a network (network_<id>_instance_<instID>_<devName>).
func OwnerInstanceNetwork(networkID int64, instanceID int, deviceName string) string {
	return fmt.Sprintf("%s_%d_%s_%d_%s", ownerNetworkPrefix, networkID, ownerInstancePrefix, instanceID, deviceName)
}

// ownerMatches reports whether pathOwner belongs to peerOwner, either exactly or
// as one of its sub-owners (e.g. peerOwner "network_1" matches "network_1",
// "network_1_forward", "network_1_load_balancer" and
// "network_1_instance_<instID>_<devName>").
func ownerMatches(peerOwner string, pathOwner string) bool {
	return pathOwner == peerOwner || strings.HasPrefix(pathOwner, peerOwner+ownerSeparator)
}
