package nodes

import (
	"fmt"

	"github.com/canonical/lxd/shared/api"
	"github.com/r3labs/diff/v3"
)

var (
	NetworkZonePrefix       = "network-zone_"
	NetworkZoneRecordPrefix = "network-zone-record_"
	NetworkPeerPrefix       = "network-peer_"
	NetworkACLPrefix        = "network-acl_"
	NetworkForwardPrefix    = "network-forward_"
	NetworkLBPrefix         = "network-lb_"
	NetworkPrefix           = "network_"
)

type NetworkZoneNode struct {
	baseNode

	// Note: a Network Zone is project independent and are globally unique.

	Name string
}

func (nz *NetworkZoneNode) Diff(n any) (diff.Changelog, error) {
	return nil, nil
}

func GenerateNetworkZoneHumanID(name string) string {
	return fmt.Sprintf("%s%s", NetworkZonePrefix, name)
}

func NewNetworkZoneNode(name string, data api.NetworkZone, id int64) *NetworkZoneNode {
	// We don't need this field to represent the inner data in the graph.
	// The 'used by' relationships are already represented and exploitable in the graph topology.
	data.UsedBy = nil
	return &NetworkZoneNode{
		baseNode: baseNode{
			data:    data,
			id:      id,
			humanID: GenerateNetworkZoneHumanID(name),
		},
		Name: name,
	}
}

type NetworkZoneRecordNode struct {
	baseNode

	ZoneName string
	Name     string
}

func (nzr *NetworkZoneRecordNode) Diff(n any) (diff.Changelog, error) {
	return nil, nil
}

func GenerateNetworkZoneRecordHumanID(zoneName string, name string) string {
	return fmt.Sprintf("%s%s_%s", NetworkZoneRecordPrefix, zoneName, name)
}

func NewNetworkZoneRecordNode(zoneName string, name string, data api.NetworkZoneRecord, id int64) *NetworkZoneRecordNode {
	return &NetworkZoneRecordNode{
		baseNode: baseNode{
			data:    data,
			id:      id,
			humanID: GenerateNetworkZoneRecordHumanID(zoneName, name),
		},
		ZoneName: zoneName,
		Name:     name,
	}
}

type NetworkPeerNode struct {
	baseNode

	Project     string
	NetworkName string
	PeerName    string
}

func (np *NetworkPeerNode) Diff(n any) (diff.Changelog, error) {
	return nil, nil
}

func GenerateNetworkPeerHumanID(project string, networkName string, peerName string) string {
	return fmt.Sprintf("%s%s_%s_%s", NetworkPeerPrefix, project, networkName, peerName)
}

func NewNetworkPeerNode(project string, networkName string, peerName string, data api.NetworkPeer, id int64) *NetworkPeerNode {
	// We don't need this field to represent the inner data in the graph.
	// The 'used by' relationships are already represented and exploitable in the graph topology.
	data.UsedBy = nil
	return &NetworkPeerNode{
		baseNode: baseNode{
			data:    data,
			id:      id,
			humanID: GenerateNetworkPeerHumanID(project, networkName, peerName),
		},
		Project:     project,
		NetworkName: networkName,
		PeerName:    peerName,
	}
}

type NetworkACLNode struct {
	baseNode

	Project string
	Name    string
}

func (acl *NetworkACLNode) Diff(n any) (diff.Changelog, error) {
	return nil, nil
}

func (n *NetworkACLNode) Renamable() bool {
	return true
}

func GenerateNetworkACLHumanID(project string, aclName string) string {
	return fmt.Sprintf("%s%s_%s", NetworkACLPrefix, project, aclName)
}

func NewNetworkACLNode(project string, aclName string, data api.NetworkACL, id int64) *NetworkACLNode {
	data.UsedBy = nil
	return &NetworkACLNode{
		baseNode: baseNode{
			data:    data,
			id:      id,
			humanID: GenerateNetworkACLHumanID(project, aclName),
		},
		Project: project,
		Name:    aclName,
	}
}

type NetworkForwardNode struct {
	baseNode

	Project     string
	NetworkName string
}

func (nf *NetworkForwardNode) Diff(n any) (diff.Changelog, error) {
	return nil, nil
}

func GenerateNetworkForwardHumanID(project string, networkName string, dataID string) string {
	return fmt.Sprintf("%s%s_%s_%s", NetworkForwardPrefix, project, networkName, dataID)
}

func NewNetworkForwardNode(project string, networkName string, data api.NetworkForward, id int64) *NetworkForwardNode {
	return &NetworkForwardNode{
		baseNode: baseNode{
			data:    data,
			id:      id,
			humanID: GenerateNetworkForwardHumanID(project, networkName, string(id)),
		},
		Project:     project,
		NetworkName: networkName,
	}
}

type NetworkLBNode struct {
	baseNode

	Project     string
	NetworkName string
}

func (lb *NetworkLBNode) Diff(n any) (diff.Changelog, error) {
	return nil, nil
}

func GenerateNetworkLBHumanID(project string, networkName string, dataID string) string {
	return fmt.Sprintf("%s%s_%s_%s", NetworkLBPrefix, project, networkName, dataID)
}

func NewNetworkLBNode(project string, networkName string, data api.NetworkLoadBalancer, id int64) *NetworkLBNode {
	return &NetworkLBNode{
		baseNode: baseNode{
			data:    data,
			id:      id,
			humanID: GenerateNetworkLBHumanID(project, networkName, string(id)),
		},
		Project:     project,
		NetworkName: networkName,
	}
}

type NetworkNode struct {
	baseNode

	Project string
	Name    string
}

func (n *NetworkNode) Diff(other any) (diff.Changelog, error) {
	return nil, nil
}

func (n *NetworkNode) Renamable() bool {
	return true
}

func NewNetworkNode(project string, name string, data api.Network, id int64) *NetworkNode {
	// We don't need this field to represent the inner data in the graph.
	// The 'used by' relationships are already represented and exploitable in the graph topology.
	data.UsedBy = nil
	return &NetworkNode{
		baseNode: baseNode{
			data:    data,
			id:      id,
			humanID: GenerateNetworkHumanID(project, name),
		},
		Project: project,
		Name:    name,
	}
}

func GenerateNetworkHumanID(project, name string) string {
	return fmt.Sprintf("%s%s_%s", NetworkPrefix, project, name)
}
