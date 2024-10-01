package core

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"

	"github.com/canonical/lxd/lxd-export/core/nodes"
	"github.com/canonical/lxd/shared/api"
	"gonum.org/v1/gonum/graph"
	"gonum.org/v1/gonum/graph/simple"
)

type StorableVertex struct {
	ID      int64  `json:"id"`
	HumanID string `json:"hid"`
	Rank    uint   `json:"rank"`
	Data    any    `json:"data"`
}

type StorableEdge struct {
	SrcID int64 `json:"s"`
	DstID int64 `json:"d"`
}

type StorableDAG struct {
	StorableVertices []StorableVertex `json:"entities"`
	StorableEdges    []StorableEdge   `json:"relationships"`
}

type MarshalVisitor struct {
	dag         *simple.DirectedGraph
	startNodeID int64
	StorableDAG
}

func NewMarshalVisitor(dag *simple.DirectedGraph, startNodeID int64) *MarshalVisitor {
	return &MarshalVisitor{dag: dag, startNodeID: startNodeID}
}

func (mv *MarshalVisitor) Visit(v StorableVertex) {
	mv.StorableVertices = append(mv.StorableVertices, v)
	children := graph.NodesOf(mv.dag.From(v.ID))
	sortedChildren := sortChildrenVertices(children)
	for _, child := range sortedChildren {
		dstID := child.ID()
		e := StorableEdge{SrcID: v.ID, DstID: dstID}
		mv.StorableEdges = append(mv.StorableEdges, e)
	}
}

func sortChildrenVertices(ns []graph.Node) []graph.Node {
	nodeList := make([]nodes.Node, 0, len(ns))
	for _, n := range ns {
		cNode, ok := n.(nodes.Node)
		if ok {
			nodeList = append(nodeList, cNode)
		}
	}

	// Sort nodes
	sort.Slice(nodeList, func(i, j int) bool {
		typeI := reflect.TypeOf(nodeList[i]).String()
		typeJ := reflect.TypeOf(nodeList[j]).String()
		if typeI == typeJ {
			return nodeList[i].HumanID() < nodeList[j].HumanID()
		}

		switch {
		case typeI == "*nodes.StoragePoolNode":
			return true
		case typeI == "*nodes.ProjectNode":
			return typeJ != "*nodes.StoragePoolNode"
		case typeI == "*nodes.NetworkZoneNode":
			return true
		case typeI == "*nodes.NetworkACLNode":
			return typeJ != "*nodes.NetworkZoneNode"
		case typeI == "*nodes.NetworkNode":
			return typeJ != "*nodes.NetworkZoneNode" && typeJ != "*nodes.NetworkACLNode"
		case typeI == "*nodes.StorageBucketNode":
			return typeJ != "*nodes.NetworkNode" && typeJ != "*nodes.NetworkZoneNode" && typeJ != "*nodes.NetworkACLNode"
		case typeI == "*nodes.StorageVolumeNode":
			return typeJ != "*nodes.StorageBucketNode" && typeJ != "*nodes.NetworkNode" && typeJ != "*nodes.NetworkZoneNode" && typeJ != "*nodes.NetworkACLNode"
		case typeI == "*nodes.ProfileNode":
			return typeJ != "*nodes.StorageVolumeNode" && typeJ != "*nodes.StorageBucketNode" && typeJ != "*nodes.NetworkNode" && typeJ != "*nodes.NetworkZoneNode" && typeJ != "*nodes.NetworkACLNode"
		case typeI == "*nodes.DeviceNode":
			return typeJ != "*nodes.ProfileNode" && typeJ != "*nodes.StorageVolumeNode" && typeJ != "*nodes.StorageBucketNode" && typeJ != "*nodes.NetworkNode" && typeJ != "*nodes.NetworkZoneNode" && typeJ != "*nodes.NetworkACLNode"
		default:
			return true
		}
	})

	// Convert back to []graph.Node
	sortedNodes := make([]graph.Node, len(nodeList))
	for i, n := range nodeList {
		sortedNodes[i] = n
	}

	return sortedNodes
}

// BFSWalk implements the Breadth-First-Search algorithm to traverse the entire DAG.
// It starts at the tree root and explores all nodes at the present depth prior
// to moving on to the nodes at the next depth level.
func BFSWalk(dag *simple.DirectedGraph, visitor *MarshalVisitor) error {
	queue := []StorableVertex{}
	startNode := dag.Node(visitor.startNodeID)
	if startNode == nil {
		return fmt.Errorf("Start node with ID %d not found", visitor.startNodeID)
	}

	cNode, ok := startNode.(nodes.Node)
	if !ok {
		return fmt.Errorf("Start node with ID %d does not implement Node interface", startNode.ID())
	}

	sv := StorableVertex{
		ID:      cNode.ID(),
		HumanID: cNode.HumanID(),
		Rank:    cNode.Rank(),
		Data:    cNode.Data(),
	}

	queue = append(queue, sv)
	visited := make(map[int64]struct{})
	for len(queue) > 0 {
		sv := queue[0]
		queue = queue[1:]
		_, ok = visited[sv.ID]
		if !ok {
			visited[sv.ID] = struct{}{}
			visitor.Visit(sv)
		}

		children := graph.NodesOf(dag.From(sv.ID))
		sortedChildren := sortChildrenVertices(children)
		for _, child := range sortedChildren {
			cChild, ok := child.(nodes.Node)
			if !ok {
				return fmt.Errorf("Child node with ID %d does not implement Node interface", child.ID())
			}

			sv = StorableVertex{
				ID:      cChild.ID(),
				HumanID: cChild.HumanID(),
				Rank:    cChild.Rank(),
				Data:    cChild.Data(),
			}

			queue = append(queue, sv)
		}
	}

	return nil
}

func MarshalJSON(dag *simple.DirectedGraph, startNodeID int64, filename string) error {
	mv := NewMarshalVisitor(dag, startNodeID)
	BFSWalk(dag, mv)

	out, err := os.Create(filename)
	if err != nil {
		return err
	}

	defer out.Close()
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "	")
	err = encoder.Encode(mv.StorableDAG)
	if err != nil {
		return err
	}

	return nil
}

func buildNodeFromVertex(sv StorableVertex) (nodes.Node, error) {
	prefix, parts := nodes.HumanIDDecode(sv.HumanID)
	humanID := sv.HumanID
	rank := sv.Rank
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

		return nodes.NewRootNodeFromData(rootData, rank), nil

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

		return nodes.NewProjectNode(parts[0], projectData, rank), nil

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

		return nodes.NewNetworkZoneNode(parts[0], zoneData, rank), nil
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

		return nodes.NewNetworkZoneRecordNode(parts[0], parts[1], zoneRecordData, rank), nil
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

		return nodes.NewNetworkPeerNode(parts[0], parts[1], parts[2], peerData, rank), nil
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

		return nodes.NewNetworkACLNode(parts[0], parts[1], aclData, rank), nil
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

		return nodes.NewNetworkForwardNode(parts[0], parts[1], forwardData, rank), nil
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

		return nodes.NewNetworkLBNode(parts[0], parts[1], lbData, rank), nil
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

		return nodes.NewNetworkNode(parts[0], parts[1], networkData, rank), nil

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

		return nodes.NewStoragePoolNode(parts[0], &poolData, rank), nil
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

		return nodes.NewStorageVolumeNode(parts[0], parts[1], parts[2], parts[3], volumeData, rank), nil
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

		return nodes.NewStorageVolumeSnapshotNode(parts[0], parts[1], parts[2], parts[3], parts[4], snapshotData, rank), nil
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

		return nodes.NewStorageBucketNode(parts[0], parts[1], parts[2], parts[3], bucketData, rank), nil
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

		return nodes.NewStorageBucketKeyNode(parts[0], parts[1], parts[2], parts[3], parts[4], bucketKeyData, rank), nil

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

		return nodes.NewProfileNode(parts[0], parts[1], profileData, rank), nil
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

		return nodes.NewDeviceNode(parts[0], parts[1], parts[2], parts[3], deviceData, rank), nil
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

		return nodes.NewInstanceNode(parts[0], parts[1], instanceData, rank), nil
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

	for _, e := range storableDAG.StorableEdges {
		dag.SetEdge(dag.NewEdge(dag.Node(e.SrcID), dag.Node(e.DstID)))
	}

	return dag, humanIDtoGraphID, nil
}
