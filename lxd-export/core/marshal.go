package core

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/canonical/lxd/lxd-export/core/nodes"
	"github.com/canonical/lxd/shared/api"
	"gonum.org/v1/gonum/graph"
	"gonum.org/v1/gonum/graph/simple"
)

type StorableVertex struct {
	ID      int64    `json:"id"`
	HumanID string   `json:"hid"`
	Data    any      `json:"data"`
	UsedBy  []string `json:"used_by"`
}

type StorableDAG struct {
	StorableVertices []StorableVertex `json:"entities"`
}

func sortVertices(nodeList []nodes.Node) {
	// Sort nodes
	sort.Slice(nodeList, func(i, j int) bool {
		return nodeList[i].ID() < nodeList[j].ID()
	})
}

func sortChildrenHIDs(hids []string) {
	sort.Slice(hids, func(i, j int) bool {
		switch {
		case strings.HasPrefix(hids[i], nodes.StoragePoolPrefix):
			if !strings.HasPrefix(hids[j], nodes.StoragePoolPrefix) {
				return true
			}

			return hids[i] < hids[j]
		case strings.HasPrefix(hids[i], nodes.ProjectPrefix):
			if !strings.HasPrefix(hids[j], nodes.ProjectPrefix) {
				return !strings.HasPrefix(hids[j], nodes.StoragePoolPrefix)
			}

			return hids[i] < hids[j]
		case strings.HasPrefix(hids[i], nodes.NetworkZonePrefix):
			if !strings.HasPrefix(hids[j], nodes.NetworkZonePrefix) {
				return !strings.HasPrefix(hids[j], nodes.ProjectPrefix) && !strings.HasPrefix(hids[j], nodes.StoragePoolPrefix)
			}

			return hids[i] < hids[j]
		case strings.HasPrefix(hids[i], nodes.NetworkACLPrefix):
			if !strings.HasPrefix(hids[j], nodes.NetworkACLPrefix) {
				return !strings.HasPrefix(hids[j], nodes.NetworkZonePrefix)
			}

			return hids[i] < hids[j]
		case strings.HasPrefix(hids[i], nodes.NetworkPrefix):
			if !strings.HasPrefix(hids[j], nodes.NetworkPrefix) {
				return !strings.HasPrefix(hids[j], nodes.NetworkACLPrefix) && !strings.HasPrefix(hids[j], nodes.NetworkZonePrefix)
			}

			return hids[i] < hids[j]
		case strings.HasPrefix(hids[i], nodes.StorageBucketPrefix):
			if !strings.HasPrefix(hids[j], nodes.StorageBucketPrefix) {
				return !strings.HasPrefix(hids[j], nodes.NetworkPrefix) && !strings.HasPrefix(hids[j], nodes.NetworkACLPrefix) && !strings.HasPrefix(hids[j], nodes.NetworkZonePrefix)
			}

			return hids[i] < hids[j]
		case strings.HasPrefix(hids[i], nodes.StorageVolumePrefix):
			if !strings.HasPrefix(hids[j], nodes.StorageVolumePrefix) {
				return !strings.HasPrefix(hids[j], nodes.StorageBucketPrefix) && !strings.HasPrefix(hids[j], nodes.NetworkPrefix) && !strings.HasPrefix(hids[j], nodes.NetworkACLPrefix) && !strings.HasPrefix(hids[j], nodes.NetworkZonePrefix)
			}

			return hids[i] < hids[j]
		case strings.HasPrefix(hids[i], nodes.ProfilePrefix):
			if !strings.HasPrefix(hids[j], nodes.ProfilePrefix) {
				return !strings.HasPrefix(hids[j], nodes.StorageVolumePrefix) && !strings.HasPrefix(hids[j], nodes.StorageBucketPrefix) && !strings.HasPrefix(hids[j], nodes.NetworkPrefix) && !strings.HasPrefix(hids[j], nodes.NetworkACLPrefix) && !strings.HasPrefix(hids[j], nodes.NetworkZonePrefix)
			}

			return hids[i] < hids[j]
		case strings.HasPrefix(hids[i], nodes.DevicePrefix):
			if !strings.HasPrefix(hids[j], nodes.DevicePrefix) {
				return !strings.HasPrefix(hids[j], nodes.ProfilePrefix) && !strings.HasPrefix(hids[j], nodes.StorageVolumePrefix) && !strings.HasPrefix(hids[j], nodes.StorageBucketPrefix) && !strings.HasPrefix(hids[j], nodes.NetworkPrefix) && !strings.HasPrefix(hids[j], nodes.NetworkACLPrefix) && !strings.HasPrefix(hids[j], nodes.NetworkZonePrefix)
			}

			return hids[i] < hids[j]
		default:
			return true
		}
	})
}

func MarshalJSON(dag *simple.DirectedGraph, startNodeID int64, filename string) error {
	editableVertices := make([]*StorableVertex, 0)
	cNodes := make([]nodes.Node, 0)
	ns := graph.NodesOf(dag.Nodes())
	for _, n := range ns {
		cNode, ok := n.(nodes.Node)
		if !ok {
			return fmt.Errorf("Start node with ID %d does not implement Node interface", n.ID())
		}

		cNodes = append(cNodes, cNode)
	}

	// Sort vertices order (use `sortChildrenVertices` function)
	sortVertices(cNodes)
	for _, cNode := range cNodes {
		editableVertices = append(editableVertices, &StorableVertex{
			ID:      cNode.ID(),
			HumanID: cNode.HumanID(),
			Data:    cNode.Data(),
			UsedBy:  make([]string, 0),
		})
	}

	for _, v := range editableVertices {
		children := make([]string, 0)
		for _, childNode := range graph.NodesOf(dag.From(v.ID)) {
			cChildNode, ok := childNode.(nodes.Node)
			if !ok {
				return fmt.Errorf("Child node with ID %d does not implement Node interface", childNode.ID())
			}

			children = append(children, cChildNode.HumanID())
		}

		// Sort children order
		sortChildrenHIDs(children)
		v.UsedBy = children
	}

	vertices := make([]StorableVertex, 0)
	for _, v := range editableVertices {
		vertices = append(vertices, *v)
	}

	out, err := os.Create(filename)
	if err != nil {
		return err
	}

	defer out.Close()
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "	")
	err = encoder.Encode(StorableDAG{StorableVertices: vertices})
	if err != nil {
		return err
	}

	return nil
}

func buildNodeFromVertex(sv StorableVertex) (nodes.Node, error) {
	prefix, parts := nodes.HumanIDDecode(sv.HumanID)
	humanID := sv.HumanID
	data := sv.Data

	switch {
	// Root node
	case prefix == "root":
		if len(parts) != 0 {
			return nil, fmt.Errorf("Invalid humanID for RootNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var rootData nodes.RootData
		err = json.Unmarshal(jsonData, &rootData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to RootData: %v", err)
		}

		return nodes.NewRootNodeFromData(rootData, sv.ID), nil

	// Project nodes
	case prefix == nodes.ProjectPrefix:
		if len(parts) != 1 {
			return nil, fmt.Errorf("Invalid humanID for ProjectNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var projectData api.Project
		err = json.Unmarshal(jsonData, &projectData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to api.Project: %v", err)
		}

		return nodes.NewProjectNode(parts[0], projectData, sv.ID), nil

	// Network nodes
	case prefix == nodes.NetworkZonePrefix:
		if len(parts) != 1 {
			return nil, fmt.Errorf("Invalid humanID for NetworkZoneNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var zoneData api.NetworkZone
		err = json.Unmarshal(jsonData, &zoneData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to api.NetworkZone: %v", err)
		}

		return nodes.NewNetworkZoneNode(parts[0], zoneData, sv.ID), nil
	case prefix == nodes.NetworkZoneRecordPrefix:
		if len(parts) != 2 {
			return nil, fmt.Errorf("Invalid humanID for NetworkZoneRecordNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var zoneRecordData api.NetworkZoneRecord
		err = json.Unmarshal(jsonData, &zoneRecordData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to api.NetworkZoneRecord: %v", err)
		}

		return nodes.NewNetworkZoneRecordNode(parts[0], parts[1], zoneRecordData, sv.ID), nil
	case prefix == nodes.NetworkPeerPrefix:
		if len(parts) != 3 {
			return nil, fmt.Errorf("Invalid humanID for NetworkPeerNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var peerData api.NetworkPeer
		err = json.Unmarshal(jsonData, &peerData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to api.NetworkPeer: %v", err)
		}

		return nodes.NewNetworkPeerNode(parts[0], parts[1], parts[2], peerData, sv.ID), nil
	case prefix == nodes.NetworkACLPrefix:
		if len(parts) != 2 {
			return nil, fmt.Errorf("Invalid humanID for NetworkACLNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var aclData api.NetworkACL
		err = json.Unmarshal(jsonData, &aclData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to api.NetworkACL: %v", err)
		}

		return nodes.NewNetworkACLNode(parts[0], parts[1], aclData, sv.ID), nil
	case prefix == nodes.NetworkForwardPrefix:
		if len(parts) != 2 {
			return nil, fmt.Errorf("Invalid humanID for NetworkForwardNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var forwardData api.NetworkForward
		err = json.Unmarshal(jsonData, &forwardData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to api.NetworkForward: %v", err)
		}

		return nodes.NewNetworkForwardNode(parts[0], parts[1], forwardData, sv.ID), nil
	case prefix == nodes.NetworkLBPrefix:
		if len(parts) != 2 {
			return nil, fmt.Errorf("Invalid humanID for NetworkLBNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var lbData api.NetworkLoadBalancer
		err = json.Unmarshal(jsonData, &lbData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to api.NetworkLoadBalancer: %v", err)
		}

		return nodes.NewNetworkLBNode(parts[0], parts[1], lbData, sv.ID), nil
	case prefix == nodes.NetworkPrefix:
		if len(parts) != 2 {
			return nil, fmt.Errorf("Invalid humanID for NetworkNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var networkData api.Network
		err = json.Unmarshal(jsonData, &networkData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to api.Network: %v", err)
		}

		return nodes.NewNetworkNode(parts[0], parts[1], networkData, sv.ID), nil

	// Storage nodes
	case prefix == nodes.StoragePoolPrefix:
		if len(parts) != 1 {
			return nil, fmt.Errorf("Invalid humanID for StoragePoolNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var poolData api.StoragePool
		err = json.Unmarshal(jsonData, &poolData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to StoragePool: %v", err)
		}

		return nodes.NewStoragePoolNode(parts[0], &poolData, sv.ID), nil
	case prefix == nodes.StorageVolumePrefix:
		if len(parts) != 4 {
			return nil, fmt.Errorf("Invalid humanID for StorageVolumeNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var volumeData api.StorageVolume
		err = json.Unmarshal(jsonData, &volumeData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to api.StorageVolume: %v", err)
		}

		return nodes.NewStorageVolumeNode(parts[0], parts[1], parts[2], parts[3], volumeData, sv.ID), nil
	case prefix == nodes.StorageVolumeSnapshotPrefix:
		if len(parts) != 5 {
			return nil, fmt.Errorf("Invalid humanID for StorageVolumeSnapshotNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var snapshotData api.StorageVolumeSnapshot
		err = json.Unmarshal(jsonData, &snapshotData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to api.StorageVolumeSnapshot: %v", err)
		}

		return nodes.NewStorageVolumeSnapshotNode(parts[0], parts[1], parts[2], parts[3], parts[4], snapshotData, sv.ID), nil
	case prefix == nodes.StorageBucketPrefix:
		if len(parts) != 4 {
			return nil, fmt.Errorf("Invalid humanID for StorageBucketNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var bucketData api.StorageBucket
		err = json.Unmarshal(jsonData, &bucketData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to api.StorageBucket: %v", err)
		}

		return nodes.NewStorageBucketNode(parts[0], parts[1], parts[2], parts[3], bucketData, sv.ID), nil
	case prefix == nodes.StorageBucketKeyPrefix:
		if len(parts) != 5 {
			return nil, fmt.Errorf("Invalid humanID for StorageBucketKeyNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var bucketKeyData api.StorageBucketKey
		err = json.Unmarshal(jsonData, &bucketKeyData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to api.StorageBucketKey: %v", err)
		}

		return nodes.NewStorageBucketKeyNode(parts[0], parts[1], parts[2], parts[3], parts[4], bucketKeyData, sv.ID), nil

	// Instance nodes
	case prefix == nodes.ProfilePrefix:
		if len(parts) != 2 {
			return nil, fmt.Errorf("Invalid humanID for ProfileNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var profileData api.Profile
		err = json.Unmarshal(jsonData, &profileData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to api.Profile: %v", err)
		}

		return nodes.NewProfileNode(parts[0], parts[1], profileData, sv.ID), nil
	case prefix == nodes.DevicePrefix:
		if len(parts) != 4 {
			return nil, fmt.Errorf("Invalid humanID for DeviceNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var deviceData map[string]string
		err = json.Unmarshal(jsonData, &deviceData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to device: %v", err)
		}

		return nodes.NewDeviceNode(parts[0], parts[1], parts[2], parts[3], deviceData, sv.ID), nil
	case prefix == nodes.InstancePrefix:
		if len(parts) != 2 {
			return nil, fmt.Errorf("Invalid humanID for InstanceNode: %v", humanID)
		}

		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshaling data: %v", err)
		}

		var instanceData api.Instance
		err = json.Unmarshal(jsonData, &instanceData)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling to api.Instance: %v", err)
		}

		return nodes.NewInstanceNode(parts[0], parts[1], instanceData, sv.ID), nil
	default:
		return nil, fmt.Errorf("Unknown node type: %v", humanID)
	}
}

func UnmarshalJSON(data []byte) (*simple.DirectedGraph, map[string]int64, error) {
	storableDAG := StorableDAG{}
	err := json.Unmarshal(data, &storableDAG)
	if err != nil {
		return nil, nil, err
	}

	dag := simple.NewDirectedGraph()
	humanIDtoGraphID := make(map[string]int64)
	for _, v := range storableDAG.StorableVertices {
		node, err := buildNodeFromVertex(v)
		if err != nil {
			return nil, nil, err
		}

		if node.ID() != v.ID {
			return nil, nil, fmt.Errorf("Node ID hash mismatch: %d != %d", node.ID(), v.ID)
		}

		dag.AddNode(node)
		humanIDtoGraphID[node.HumanID()] = node.ID()
	}

	for _, v := range storableDAG.StorableVertices {
		for _, childHumanID := range v.UsedBy {
			childID, ok := humanIDtoGraphID[childHumanID]
			if !ok {
				return nil, nil, fmt.Errorf("Child node with humanID %s not found", childHumanID)
			}

			dag.SetEdge(dag.NewEdge(dag.Node(v.ID), dag.Node(childID)))
		}
	}

	return dag, humanIDtoGraphID, nil
}
