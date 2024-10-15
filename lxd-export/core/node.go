package core

import (
	"fmt"
	"sort"

	"github.com/canonical/lxd/shared"
	"github.com/r3labs/diff/v3"
)

// Node represents a node in the DAG
type Node struct {
	ID       int64
	HID      string
	Children map[string]*Node
	Parents  map[string]*Node
	Data     any
}

// DAG represents the Directed Acyclic Graph
type DAG struct {
	Nodes map[string]*Node
}

// NewDAG creates a new DAG
func NewDAG() *DAG {
	return &DAG{
		Nodes: make(map[string]*Node),
	}
}

// HasNode checks if a node exists in the DAG
func (d *DAG) HasNode(hid string) bool {
	_, exists := d.Nodes[hid]
	return exists
}

// HasEdge checks if an edge exists between two nodes
func (d *DAG) HasEdge(fromID, toID string) bool {
	from, ok := d.Nodes[fromID]
	if !ok {
		return false
	}

	_, exists := from.Children[toID]
	return exists
}

// AddNode adds a new node to the DAG
func (d *DAG) AddNode(id int64, hid string, data any) *Node {
	node, exists := d.Nodes[hid]
	if exists {
		return node
	}

	node = &Node{
		ID:       id,
		HID:      hid,
		Children: make(map[string]*Node),
		Parents:  make(map[string]*Node),
		Data:     data,
	}

	d.Nodes[hid] = node
	return node
}

// AddEdge adds a directed edge between two nodes
func (d *DAG) SetEdge(fromID, toID string) error {
	from, ok := d.Nodes[fromID]
	if !ok {
		return fmt.Errorf("node %s not found", fromID)
	}

	to, ok := d.Nodes[toID]
	if !ok {
		return fmt.Errorf("node %s not found", toID)
	}

	_, exists := from.Children[toID]
	if exists {
		return fmt.Errorf("edge already exists between %s and %s", fromID, toID)
	}

	from.Children[toID] = to
	to.Parents[fromID] = from
	return nil
}

// GetChildren returns the children of a node
func (d *DAG) GetChildren(hid string) ([]*Node, error) {
	node, exists := d.Nodes[hid]
	if !exists {
		return nil, fmt.Errorf("node %s not found", hid)
	}

	children := make([]*Node, 0, len(node.Children))
	for _, child := range node.Children {
		children = append(children, child)
	}

	return children, nil
}

// GetParents returns the parents of a node
func (d *DAG) GetParents(hid string) ([]*Node, error) {
	node, exists := d.Nodes[hid]
	if !exists {
		return nil, fmt.Errorf("node %s not found", hid)
	}

	parents := make([]*Node, 0, len(node.Parents))
	for _, parent := range node.Parents {
		parents = append(parents, parent)
	}

	return parents, nil
}

// GetAncestors returns the ancestors of a node, meaning all the nodes that are
// reachable by following the parents of the node.
func (d *DAG) GetAncestors(hid string) ([]*Node, error) {
	node, exists := d.Nodes[hid]
	if !exists {
		return nil, fmt.Errorf("node %s not found", hid)
	}

	var ancestors func(n *Node, visited map[string]struct{}, res []*Node) error
	ancestors = func(n *Node, visited map[string]struct{}, res []*Node) error {
		_, ok := visited[n.HID]
		if ok {
			return nil
		}

		visited[n.HID] = struct{}{}
		parents, err := d.GetParents(n.HID)
		if err != nil {
			return err
		}

		res = append(res, parents...)
		for _, parent := range parents {
			visited[parent.HID] = struct{}{}
		}

		for _, parent := range n.Parents {
			err := ancestors(parent, visited, res)
			if err != nil {
				return err
			}
		}

		return nil
	}

	visited := make(map[string]struct{})
	res := make([]*Node, 0)

	err := ancestors(node, visited, res)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// HCLDep represents an HCL dependency
type HCLDep struct {
	Label string
	ID    string
}

// HCLResourceNode represents the content of a "resource" block in an HCL file.
type HCLResourceNode struct {
	Label     string   `diff:"label"`
	ID        string   `diff:"id,identifier"`
	Data      any      `diff:"data"`
	DependsOn []HCLDep `diff:"depends_on"`
}

func sortParentHIDs(hids []string) {
	sort.Slice(hids, func(i, j int) bool {
		iPrefix, _ := humanIDDecode(hids[i])
		jPrefix, _ := humanIDDecode(hids[j])
		iImportance := prefixToImportance[iPrefix]
		jImportance := prefixToImportance[jPrefix]
		if iImportance == jImportance {
			return hids[i] < hids[j]
		}

		return iImportance < jImportance
	})
}

func getHCLLabel(hid string) (string, error) {
	prefix, _ := humanIDDecode(hid)
	switch prefix {
	case projectPrefix:
		return "lxd_project", nil
	case storagePoolPrefix:
		return "lxd_storage_pool", nil
	case storageVolumePrefix:
		return "lxd_volume", nil
	case storageVolumeSnapshotPrefix:
		return "lxd_storage_volume_snapshot", nil // not implemented in Terraform provider
	case storageBucketPrefix:
		return "lxd_storage_bucket", nil // not implemented in Terraform provider
	case storageBucketKeyPrefix:
		return "lxd_storage_bucket_key", nil // not implemented in Terraform provider
	case networkZonePrefix:
		return "lxd_network_zone", nil
	case networkZoneRecordPrefix:
		return "lxd_network_zone_record", nil
	case networkPrefix:
		return "lxd_network", nil
	case networkForwardPrefix:
		return "lxd_network_forward", nil
	case networkLBPrefix:
		return "lxd_network_lb", nil
	case networkPeerPrefix:
		return "lxd_network_peer", nil
	case networkACLPrefix:
		return "lxd_network_acl", nil
	case profilePrefix:
		return "lxd_profile", nil
	case instancePrefix:
		return "lxd_instance", nil
	default:
		return "", fmt.Errorf("unknown prefix %s", prefix)
	}
}

func getSupportedHCLLabels() []string {
	return []string{
		"lxd_project",
		"lxd_storage_pool",
		"lxd_volume",
		"lxd_network_zone",
		"lxd_network_zone_record",
		"lxd_network",
		"lxd_network_forward",
		"lxd_network_lb",
		"lxd_network_peer",
		"lxd_network_acl",
		"lxd_profile",
		"lxd_instance",
	}
}

// HCLOrder converts the DAG to a list of nodes in a stable topological order
// suitable for Terraform HCL resource serialization.
func (d *DAG) HCLOrder(l *Logger) ([]*HCLResourceNode, error) {
	ns := make([]*Node, 0, len(d.Nodes))
	for _, n := range d.Nodes {
		ns = append(ns, n)
	}

	sort.Slice(ns, func(i, j int) bool {
		return ns[i].ID < ns[j].ID
	})

	hclVertices := make([]*HCLResourceNode, 0)
	for _, n := range ns {
		label, err := getHCLLabel(n.HID)
		if err != nil {
			return nil, err
		}

		hclVertices = append(hclVertices, &HCLResourceNode{
			Label:     label,
			ID:        n.HID,
			Data:      n.Data,
			DependsOn: make([]HCLDep, 0),
		})

		l.Info("Added node to HCL order", map[string]any{"label": label, "id": n.HID})
	}

	for i, n := range ns {
		parentHIDs := make([]string, 0, len(n.Parents))
		for _, p := range n.Parents {
			parentHIDs = append(parentHIDs, p.HID)
		}

		if len(parentHIDs) > 0 {
			sortParentHIDs(parentHIDs)

			parentDeps := make([]HCLDep, 0, len(parentHIDs))
			for _, parentHID := range parentHIDs {
				parentLabel, err := getHCLLabel(parentHID)
				if err != nil {
					return nil, err
				}

				parentDeps = append(parentDeps, HCLDep{Label: parentLabel, ID: parentHID})
			}

			hclVertices[i].DependsOn = parentDeps
			l.Info("Added dependencies to node", map[string]any{"label": hclVertices[i].Label, "id": hclVertices[i].ID, "depends_on": parentHIDs})
		}
	}

	return hclVertices, nil
}

type HCLDiff struct {
	// Project
	projectChangelog diff.Changelog

	// Networking
	networkZoneChangelog       diff.Changelog
	networkZoneRecordChangelog diff.Changelog
	networkChangelog           diff.Changelog
	networkForwardChangelog    diff.Changelog
	networkLBChangelog         diff.Changelog
	networkPeerChangelog       diff.Changelog
	networkACLChangelog        diff.Changelog

	// Storage
	storagePoolChangelog   diff.Changelog
	storageVolumeChangelog diff.Changelog

	// Profile
	profileChangelog  diff.Changelog
	instanceChangelog diff.Changelog
}

func inventoriesChangelogs(srcInventory []*HCLResourceNode, dstInventory []*HCLResourceNode) (allDiffs diff.Changelog, err error) {
	allDiffs = make(diff.Changelog, 0)
	supportedLabels := getSupportedHCLLabels()

	srcProjects := make([]*HCLResourceNode, 0)
	dstProjects := make([]*HCLResourceNode, 0)

	srcNetworkZones := make([]*HCLResourceNode, 0)
	dstNetworkZones := make([]*HCLResourceNode, 0)
	srcNetworkZoneRecords := make([]*HCLResourceNode, 0)
	dstNetworkZoneRecords := make([]*HCLResourceNode, 0)
	srcNetworks := make([]*HCLResourceNode, 0)
	dstNetworks := make([]*HCLResourceNode, 0)
	srcNetworkForwards := make([]*HCLResourceNode, 0)
	dstNetworkForwards := make([]*HCLResourceNode, 0)
	srcNetworkLBs := make([]*HCLResourceNode, 0)
	dstNetworkLBs := make([]*HCLResourceNode, 0)
	srcNetworkPeers := make([]*HCLResourceNode, 0)
	dstNetworkPeers := make([]*HCLResourceNode, 0)
	srcNetworkACLs := make([]*HCLResourceNode, 0)
	dstNetworkACLs := make([]*HCLResourceNode, 0)

	srcStoragePools := make([]*HCLResourceNode, 0)
	dstStoragePools := make([]*HCLResourceNode, 0)
	srcStorageVolumes := make([]*HCLResourceNode, 0)
	dstStorageVolumes := make([]*HCLResourceNode, 0)

	srcProfiles := make([]*HCLResourceNode, 0)
	dstProfiles := make([]*HCLResourceNode, 0)
	srcInstances := make([]*HCLResourceNode, 0)
	dstInstances := make([]*HCLResourceNode, 0)

	fillData := func(inventory []*HCLResourceNode, src bool) error {
		for _, n := range inventory {
			if !shared.ValueInSlice[string](n.Label, supportedLabels) {
				fmt.Printf("Unsupported resource type: %q . Ignoring.\n", n.Label)
				continue
			}

			switch n.Label {
			case "lxd_project":
				if src {
					srcProjects = append(srcProjects, n)
				} else {
					dstProjects = append(dstProjects, n)
				}
			case "lxd_network_zone":
				if src {
					srcNetworkZones = append(srcNetworkZones, n)
				} else {
					dstNetworkZones = append(dstNetworkZones, n)
				}
			case "lxd_network_zone_record":
				if src {
					srcNetworkZoneRecords = append(srcNetworkZoneRecords, n)
				} else {
					dstNetworkZoneRecords = append(dstNetworkZoneRecords, n)
				}
			case "lxd_network":
				if src {
					srcNetworks = append(srcNetworks, n)
				} else {
					dstNetworks = append(dstNetworks, n)
				}
			case "lxd_network_forward":
				if src {
					srcNetworkForwards = append(srcNetworkForwards, n)
				} else {
					dstNetworkForwards = append(dstNetworkForwards, n)
				}
			case "lxd_network_lb":
				if src {
					srcNetworkLBs = append(srcNetworkLBs, n)
				} else {
					dstNetworkLBs = append(dstNetworkLBs, n)
				}
			case "lxd_network_peer":
				if src {
					srcNetworkPeers = append(srcNetworkPeers, n)
				} else {
					dstNetworkPeers = append(dstNetworkPeers, n)
				}
			case "lxd_network_acl":
				if src {
					srcNetworkACLs = append(srcNetworkACLs, n)
				} else {
					dstNetworkACLs = append(dstNetworkACLs, n)
				}
			case "lxd_storage_pool":
				if src {
					srcStoragePools = append(srcStoragePools, n)
				} else {
					dstStoragePools = append(dstStoragePools, n)
				}
			case "lxd_volume":
				if src {
					srcStorageVolumes = append(srcStorageVolumes, n)
				} else {
					dstStorageVolumes = append(dstStorageVolumes, n)
				}
			case "lxd_profile":
				if src {
					srcProfiles = append(srcProfiles, n)
				} else {
					dstProfiles = append(dstProfiles, n)
				}
			case "lxd_instance":
				if src {
					srcInstances = append(srcInstances, n)
				} else {
					dstInstances = append(dstInstances, n)
				}
			}
		}

		return nil
	}

	err = fillData(srcInventory, true)
	if err != nil {
		return nil, fmt.Errorf("Error filling source data: %w", err)
	}

	err = fillData(dstInventory, false)
	if err != nil {
		return nil, fmt.Errorf("Error filling destination data: %w", err)
	}

	differ, err := diff.NewDiffer(diff.DisableStructValues())
	if err != nil {
		return nil, fmt.Errorf("Failed to create differ: %w", err)
	}

	projectChangelog, err := differ.Diff(srcProjects, dstProjects)
	if err != nil {
		return nil, fmt.Errorf("Failed to diff project nodes: %w", err)
	}

	allDiffs = append(allDiffs, projectChangelog...)

	networkZoneChangelog, err := differ.Diff(srcNetworkZones, dstNetworkZones)
	if err != nil {
		return nil, fmt.Errorf("Failed to diff network zone nodes: %w", err)
	}

	allDiffs = append(allDiffs, networkZoneChangelog...)

	networkZoneRecordChangelog, err := differ.Diff(srcNetworkZoneRecords, dstNetworkZoneRecords)
	if err != nil {
		return nil, fmt.Errorf("Failed to diff network zone record nodes: %w", err)
	}

	allDiffs = append(allDiffs, networkZoneRecordChangelog...)

	networkChangelog, err := differ.Diff(srcNetworks, dstNetworks)
	if err != nil {
		return nil, fmt.Errorf("Failed to diff network nodes: %w", err)
	}

	allDiffs = append(allDiffs, networkChangelog...)

	networkForwardChangelog, err := differ.Diff(srcNetworkForwards, dstNetworkForwards)
	if err != nil {
		return nil, fmt.Errorf("Failed to diff network forward nodes: %w", err)
	}

	allDiffs = append(allDiffs, networkForwardChangelog...)

	networkLBChangelog, err := differ.Diff(srcNetworkLBs, dstNetworkLBs)
	if err != nil {
		return nil, fmt.Errorf("Failed to diff network load balancer nodes: %w", err)
	}

	allDiffs = append(allDiffs, networkLBChangelog...)

	networkPeerChangelog, err := differ.Diff(srcNetworkPeers, dstNetworkPeers)
	if err != nil {
		return nil, fmt.Errorf("Failed to diff network peer nodes: %w", err)
	}

	allDiffs = append(allDiffs, networkPeerChangelog...)

	networkACLChangelog, err := differ.Diff(srcNetworkACLs, dstNetworkACLs)
	if err != nil {
		return nil, fmt.Errorf("Failed to diff network ACL nodes: %w", err)
	}

	allDiffs = append(allDiffs, networkACLChangelog...)

	storagePoolChangelog, err := differ.Diff(srcStoragePools, dstStoragePools)
	if err != nil {
		return nil, fmt.Errorf("Failed to diff storage pool nodes: %w", err)
	}

	allDiffs = append(allDiffs, storagePoolChangelog...)

	storageVolumeChangelog, err := differ.Diff(srcStorageVolumes, dstStorageVolumes)
	if err != nil {
		return nil, fmt.Errorf("Failed to diff storage volume nodes: %w", err)
	}

	allDiffs = append(allDiffs, storageVolumeChangelog...)

	profileChangelog, err := differ.Diff(srcProfiles, dstProfiles)
	if err != nil {
		return nil, fmt.Errorf("Failed to diff profile nodes: %w", err)
	}

	allDiffs = append(allDiffs, profileChangelog...)

	instanceChangelog, err := differ.Diff(srcInstances, dstInstances)
	if err != nil {
		return nil, fmt.Errorf("Failed to diff instance nodes: %w", err)
	}

	allDiffs = append(allDiffs, instanceChangelog...)
	return allDiffs, nil
}
