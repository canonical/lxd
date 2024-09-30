package importer

import (
	"fmt"

	"github.com/canonical/lxd/lxd-export/core/nodes"
)

// queryNodePair queries the source and target DAGs for the nodes with the given human IDs.
// The nodes are expected to be of the same type.
// This is useful to fetch two nodes that have the same human ID in the source and target DAGs (a common case).
func queryNodePair[
	// Root
	N *nodes.RootNode |
		// Project
		*nodes.ProjectNode |
		// Storage
		*nodes.StoragePoolNode |
		*nodes.StorageVolumeNode |
		*nodes.StorageVolumeSnapshotNode |
		*nodes.StorageBucketNode |
		*nodes.StorageBucketKeyNode |
		// Network
		*nodes.NetworkZoneNode |
		*nodes.NetworkZoneRecordNode |
		*nodes.NetworkPeerNode |
		*nodes.NetworkACLNode |
		*nodes.NetworkLBNode |
		*nodes.NetworkForwardNode |
		*nodes.NetworkNode |
		// Instance
		*nodes.DeviceNode |
		*nodes.ProfileNode |
		*nodes.InstanceNode](p *Planner, sHID string, tHID string) (sNode N, tNode N, err error) {

	s, ok := p.srcHIDtoID[sHID]
	if !ok {
		return sNode, tNode, fmt.Errorf("Source node %q not found", sHID)
	}

	t, ok := p.dstHIDtoID[tHID]
	if !ok {
		return sNode, tNode, fmt.Errorf("Target node %q not found", tHID)
	}

	sNode, ok = p.srcDAG.Node(s).(N)
	if !ok {
		return sNode, tNode, fmt.Errorf("Source node %q is not a a node of type %T", sHID, *new(N))
	}

	tNode, ok = p.dstDAG.Node(t).(N)
	if !ok {
		return sNode, tNode, fmt.Errorf("Target node %q is not a a node of type %T", tHID, *new(N))
	}

	return sNode, tNode, nil
}
