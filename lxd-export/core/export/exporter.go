package export

import (
	"errors"
	"fmt"
	"strings"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd-export/core/logger"
	"github.com/canonical/lxd/lxd-export/core/nodes"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"gonum.org/v1/gonum/graph/simple"
)

func getFeaturesPerProject(projects []api.Project) map[string]map[string]string {
	res := make(map[string]map[string]string)
	for _, project := range projects {
		name := project.Name
		features := make(map[string]string)
		for k, v := range project.Config {
			if strings.HasPrefix(k, "features.") {
				features[k] = v
			}
		}

		res[name] = features
	}

	return res
}

func addNetworkZones(
	client lxd.InstanceServer,
	projectNode *nodes.ProjectNode,
	graph *simple.DirectedGraph,
	humanIDtoGraphID map[string]int64,
	nzs []api.NetworkZone,
	nodeID *int64,
) error {
	for _, nz := range nzs {
		nzNode := nodes.NewNetworkZoneNode(nz.Name, nz, *nodeID)
		graph.AddNode(nzNode)
		graph.SetEdge(graph.NewEdge(projectNode, nzNode))
		humanIDtoGraphID[nzNode.HumanID()] = nzNode.ID()
		*nodeID++

		// Add network zone records (only dependent on their network zone)
		records, err := client.GetNetworkZoneRecords(nz.Name)
		if err != nil {
			return err
		}

		for _, record := range records {
			recordNode := nodes.NewNetworkZoneRecordNode(nz.Name, record.Name, record, *nodeID)
			graph.AddNode(recordNode)
			graph.SetEdge(graph.NewEdge(nzNode, recordNode))
			humanIDtoGraphID[recordNode.HumanID()] = recordNode.ID()
			*nodeID++
		}
	}

	return nil
}

func addNetworkLBs(
	client lxd.InstanceServer,
	projectName string,
	graph *simple.DirectedGraph,
	netNode *nodes.NetworkNode,
	humanIDtoGraphID map[string]int64,
	nodeID *int64,
) error {
	lbs, err := client.GetNetworkLoadBalancers(netNode.Name)
	if err != nil {
		return err
	}

	for _, lb := range lbs {
		lbNode := nodes.NewNetworkLBNode(projectName, netNode.Name, lb, *nodeID)
		*nodeID++
		_, exists := humanIDtoGraphID[lbNode.HumanID()]
		if exists {
			continue
		}

		humanIDtoGraphID[lbNode.HumanID()] = lbNode.ID()
		graph.AddNode(lbNode)
		graph.SetEdge(graph.NewEdge(netNode, lbNode))
	}

	return nil
}

func addNetworkForwards(
	client lxd.InstanceServer,
	projectName string,
	graph *simple.DirectedGraph,
	netNode *nodes.NetworkNode,
	humanIDtoGraphID map[string]int64,
	nodeID *int64,
) error {
	forwards, err := client.GetNetworkForwards(netNode.Name)
	if err != nil {
		return err
	}

	for _, forward := range forwards {
		forwardNode := nodes.NewNetworkForwardNode(projectName, netNode.Name, forward, *nodeID)
		*nodeID++
		_, exists := humanIDtoGraphID[forwardNode.HumanID()]
		if exists {
			continue
		}

		humanIDtoGraphID[forwardNode.HumanID()] = forwardNode.ID()
		graph.AddNode(forwardNode)
		graph.SetEdge(graph.NewEdge(netNode, forwardNode))
	}

	return nil
}

func addNetworks(
	client lxd.InstanceServer,
	projectName string,
	projectNode *nodes.ProjectNode,
	graph *simple.DirectedGraph,
	humanIDtoGraphID map[string]int64,
	networks []api.Network,
	inheritedNetworks []api.Network,
	nodeID *int64,
) error {
	children := make(map[string][]string)
	parents := make(map[string]string) // a network can have only one or zero parent
	inDegree := make(map[string]int)
	netMap := make(map[string]api.Network)

	initNetConnectivity := func(netIdx int) {
		children[networks[netIdx].Name] = []string{}
		inDegree[networks[netIdx].Name] = 0
		netMap[networks[netIdx].Name] = networks[netIdx]
	}

	for i, net := range networks {
		c := net.Config
		if net.Type == "bridge" {
			initNetConnectivity(i)

			// There is no dependency for this type of network.
			continue
		} else if net.Type == "ovn" {
			initNetConnectivity(i)

			if c["network"] != "" {
				children[c["network"]] = append(children[c["network"]], net.Name)
				parents[net.Name] = c["network"]
				inDegree[net.Name]++
			}
		} else if net.Type == "physical" {
			initNetConnectivity(i)

			if c["parent"] != "" {
				children[c["parent"]] = append(children[c["parent"]], net.Name)
				parents[net.Name] = c["parent"]
				inDegree[net.Name]++
			}
		} else if net.Type == "macvlan" {
			initNetConnectivity(i)

			if c["parent"] != "" {
				children[c["parent"]] = append(children[c["parent"]], net.Name)
				parents[net.Name] = c["parent"]
				inDegree[net.Name]++
			}
		} else if net.Type == "sriov" {
			initNetConnectivity(i)

			if c["parent"] != "" {
				children[c["parent"]] = append(children[c["parent"]], net.Name)
				parents[net.Name] = c["parent"]
				inDegree[net.Name]++
			}
		}
	}

	// Find nodes with no incoming edges (no parents)
	var queue []string
	for id, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}

	// BFS
	level := 0
	for len(queue) > 0 {
		levelSize := len(queue)
		for i := 0; i < levelSize; i++ {
			node := queue[0]
			queue = queue[1:]

			net := netMap[node]

			// Add to DAG
			// Check if the network is dependent on a network zone
			var nzFound bool
			var nzNode *nodes.NetworkZoneNode
			for _, nz := range []string{"dns.zone.forward", "dns.zone.reverse.ipv4", "dns.zone.reverse.ipv6"} {
				if net.Config[nz] != "" {
					nzID, exists := humanIDtoGraphID[nodes.GenerateNetworkZoneHumanID(net.Config["dns.zone.forward"])]
					if !exists {
						return fmt.Errorf("Network %q depends on a non-existing network zone %q", net.Name, net.Config["dns.zone.forward"])
					}

					nzNode, nzFound = graph.Node(nzID).(*nodes.NetworkZoneNode)
					if !nzFound {
						return errors.New("Invalid node type for network zone")
					}

					break
				}
			}

			var parentNetworkNode *nodes.NetworkNode
			if level != 0 {
				parentNetwork := parents[node]
				var parentNetworkProject string
				if projectName != "default" {
					if inheritedNetworks == nil {
						parentNetworkProject = projectName
					} else {
						for _, net := range inheritedNetworks {
							if net.Name == parentNetwork {
								parentNetworkProject = "default"
								break
							}
						}

						if parentNetworkProject == "" {
							parentNetworkProject = projectName
						}
					}
				} else {
					parentNetworkProject = "default"
				}

				parentID, exists := humanIDtoGraphID[nodes.GenerateNetworkHumanID(parentNetworkProject, parentNetwork)]
				if !exists {
					return fmt.Errorf("Network %q in project %s depends on a non-existing parent network %q", net.Name, projectName, parentNetwork)
				}

				var ok bool
				parentNetworkNode, ok = graph.Node(parentID).(*nodes.NetworkNode)
				if !ok {
					return fmt.Errorf("The parent network node %q is not in the graph", parentNetwork)
				}
			}

			netNode := nodes.NewNetworkNode(projectName, net.Name, net, *nodeID)
			*nodeID++
			graph.AddNode(netNode)
			humanIDtoGraphID[netNode.HumanID()] = netNode.ID()
			if nzFound && nzNode != nil {
				graph.SetEdge(graph.NewEdge(nzNode, netNode))
			}

			if level == 0 {
				graph.SetEdge(graph.NewEdge(projectNode, netNode))
			} else {
				// It means that the network is dependent on a parent network
				if parentNetworkNode == nil {
					return errors.New("Invalid parent network node")
				}

				graph.SetEdge(graph.NewEdge(parentNetworkNode, netNode))
			}

			if net.Managed {
				if net.Type == "ovn" {
					// Attach network load balancer dependencies to the network if any.
					err := addNetworkLBs(client, projectName, graph, netNode, humanIDtoGraphID, nodeID)
					if err != nil {
						return err
					}
				}

				if net.Type == "ovn" || net.Type == "bridge" {
					// Attach network forward dependencies to network if any.
					err := addNetworkForwards(client, projectName, graph, netNode, humanIDtoGraphID, nodeID)
					if err != nil {
						return err
					}
				}
			}

			for _, child := range children[node] {
				inDegree[child]--
				if inDegree[child] == 0 {
					queue = append(queue, child)
				}
			}
		}

		level++
	}

	return nil
}

func addNetworkPeers(
	client lxd.InstanceServer,
	projectName string,
	graph *simple.DirectedGraph,
	humanIDtoGraphID map[string]int64,
	nodeID *int64,
) error {
	// Get cached network nodes for all projects. This has the advantage of working on a local
	// copy of `humanIDtoGraphID` that is shared between all the workers and to minimize lock/unlock.
	networkIDs := make(map[string]int64)
	for humanID, id := range humanIDtoGraphID {
		if strings.HasPrefix(humanID, nodes.NetworkPrefix) {
			networkIDs[humanID] = id
		}
	}

	networksPrefixPerProject := fmt.Sprintf("%s%s_", nodes.NetworkPrefix, projectName)
	for humanID, id := range networkIDs {
		if !strings.HasPrefix(humanID, networksPrefixPerProject) {
			continue
		}

		// Get the source network node
		netNode, ok := graph.Node(id).(*nodes.NetworkNode)
		if !ok {
			return fmt.Errorf("The network node %q is not in the graph", humanID)
		}

		netData, ok := netNode.Data().(api.Network)
		if !ok {
			return fmt.Errorf("Invalid network data type")
		}

		// Only managed 'ovn' networks support peering.
		if netData.Managed && netData.Type == "ovn" {
			// Check if it has peers
			peers, err := client.GetNetworkPeers(netNode.Name)
			if err != nil {
				return err
			}

			for _, peer := range peers {
				// Check target network and project
				var targetNetworkNode *nodes.NetworkNode
				targetNetworkHumanID := nodes.GenerateNetworkHumanID(peer.TargetProject, peer.TargetNetwork)
				for targetHumanID, targetID := range networkIDs {
					if targetHumanID == targetNetworkHumanID {
						targetNetworkNode, ok = graph.Node(targetID).(*nodes.NetworkNode)
						if !ok {
							return fmt.Errorf("The target network node %q for the peer %q is not in the graph", targetHumanID, peer.Name)
						}

						break
					}
				}

				if targetNetworkNode == nil {
					return fmt.Errorf("No target network could be found for the peer %s", peer.Name)
				}

				netPeerNode := nodes.NewNetworkPeerNode(projectName, netNode.Name, peer.Name, peer, *nodeID)
				*nodeID++
				graph.AddNode(netPeerNode)
				humanIDtoGraphID[nodes.GenerateNetworkPeerHumanID(projectName, netNode.Name, peer.Name)] = netPeerNode.ID()

				graph.SetEdge(graph.NewEdge(netNode, netPeerNode))
				graph.SetEdge(graph.NewEdge(targetNetworkNode, netPeerNode))
			}
		}
	}

	return nil
}

func addNetworkACLs(
	client lxd.InstanceServer,
	projectName string,
	projectNode *nodes.ProjectNode,
	graph *simple.DirectedGraph,
	humanIDtoGraphID map[string]int64,
	nodeID *int64,
) error {
	acls, err := client.GetNetworkACLs()
	if err != nil {
		return err
	}

	if len(acls) == 0 {
		return nil
	}

	// Get a local copy of the peer node references,
	// as an ACL can be dependant on a network peer if an ACL rule
	// uses a network subject selector
	projectNetworkPeerPrefix := fmt.Sprintf("%s%s_", nodes.NetworkPeerPrefix, projectName)
	netPeers := make(map[string]int64)
	for humanID, id := range humanIDtoGraphID {
		if strings.HasPrefix(humanID, projectNetworkPeerPrefix) {
			netPeers[humanID] = id
		}
	}

	extractNetworkAndPeer := func(input string) (string, string, error) {
		input = strings.TrimPrefix(input, "@")
		parts := strings.Split(input, "/")
		if len(parts) != 2 {
			return "", "", fmt.Errorf("invalid format: expected '@<network_name>/<peer_name>', got %q", input)
		}

		networkName := parts[0]
		peerName := parts[1]
		if networkName == "" || peerName == "" {
			return "", "", fmt.Errorf("invalid format: network_name and peer_name cannot be empty")
		}

		return networkName, peerName, nil
	}

	peerIDs := make([]int64, 0)
	for _, acl := range acls {
		ingressRules := acl.Ingress
		for _, rule := range ingressRules {
			if strings.HasPrefix(rule.Source, "@") {
				if rule.Source == "@internal" || rule.Source == "@external" {
					continue
				}

				// Else, it means that the source is a 'network subject selector'
				// in the format '@<network_name>/<peer_name>' . An ACL can then depends on a 'network peer' node.
				// the peer is always in the same project.
				networkName, peerName, err := extractNetworkAndPeer(rule.Source)
				if err != nil {
					return err
				}

				// Attempt to find the right network peer node reference
				peerID, exists := netPeers[nodes.GenerateNetworkPeerHumanID(projectName, networkName, peerName)]
				if !exists {
					return fmt.Errorf("Could not find the network peer (%s/%s) node reference for ACL ingress source rule %q", networkName, peerName, acl.Name)
				}

				peerIDs = append(peerIDs, peerID)
			}
		}

		egressRules := acl.Egress
		for _, rule := range egressRules {
			if strings.HasPrefix(rule.Destination, "@") {
				if rule.Destination == "@internal" || rule.Destination == "@external" {
					continue
				}

				networkName, peerName, err := extractNetworkAndPeer(rule.Destination)
				if err != nil {
					return err
				}

				peerID, exists := netPeers[nodes.GenerateNetworkPeerHumanID(projectName, networkName, peerName)]
				if !exists {
					return fmt.Errorf("Could not find the network peer (%s/%s) node reference for ACL egress destination rule %q", networkName, peerName, acl.Name)
				}

				peerIDs = append(peerIDs, peerID)
			}
		}

		if len(peerIDs) == 0 {
			// In this case, if an ACL rules are not dependant on any network peers,
			// then it is dependent of the root of this worker (the 'project' node).
			aclNode := nodes.NewNetworkACLNode(projectName, acl.Name, acl, *nodeID)
			*nodeID++
			graph.AddNode(aclNode)
			graph.SetEdge(graph.NewEdge(projectNode, aclNode))
			humanIDtoGraphID[aclNode.HumanID()] = aclNode.ID()
		} else if len(peerIDs) == 1 {
			peerNode, ok := graph.Node(peerIDs[0]).(*nodes.NetworkPeerNode)
			if !ok {
				return fmt.Errorf("Invalid node type for network peer")
			}

			aclNode := nodes.NewNetworkACLNode(projectName, acl.Name, acl, *nodeID)
			*nodeID++
			graph.AddNode(aclNode)
			graph.SetEdge(graph.NewEdge(peerNode, aclNode))
			humanIDtoGraphID[aclNode.HumanID()] = aclNode.ID()
		} else {
			// We need to find the max rank among all the network peer nodes,
			// create the network ACL node with the incremented max rank and
			// connect all the network peer nodes to the network ACL node.
			peerNodes := make([]*nodes.NetworkPeerNode, 0)
			for _, id := range peerIDs {
				peerNode, ok := graph.Node(id).(*nodes.NetworkPeerNode)
				if !ok {
					return fmt.Errorf("Invalid node type for network peer")
				}

				peerNodes = append(peerNodes, peerNode)
			}

			aclNode := nodes.NewNetworkACLNode(projectName, acl.Name, acl, *nodeID)
			*nodeID++
			graph.AddNode(aclNode)
			for _, peerNode := range peerNodes {
				graph.SetEdge(graph.NewEdge(peerNode, aclNode))
			}
		}
	}

	return nil
}

func exportNetworking(
	client lxd.InstanceServer,
	dag *simple.DirectedGraph,
	humanIDtoGraphID map[string]int64,
	defaultProjectNode *nodes.ProjectNode,
	otherProjectNodes map[string]*nodes.ProjectNode,
	featuresPerProject map[string]map[string]string,
	nodeID *int64,
) error {
	// 1) Export network zones
	nzs, err := client.GetNetworkZones()
	if err != nil {
		return err
	}

	err = addNetworkZones(client, defaultProjectNode, dag, humanIDtoGraphID, nzs, nodeID)
	if err != nil {
		return err
	}

	for projectName, projectNode := range otherProjectNodes {
		nzs, err := client.UseProject(projectName).GetNetworkZones()
		if err != nil {
			return err
		}

		if shared.IsTrue(featuresPerProject[projectName]["features.networks.zones"]) {
			err = addNetworkZones(client.UseProject(projectName), projectNode, dag, humanIDtoGraphID, nzs, nodeID)
			if err != nil {
				return err
			}
		} else {
			// it means I inherit from 'default' project network zones
			// and that some of the network zones we get there are already in the graph.
			// We need to have a filter to only add the network zones that are not already in the graph.
			filteredNetworkZones := make([]api.NetworkZone, 0)
			for _, nz := range nzs {
				_, exists := humanIDtoGraphID[nodes.GenerateNetworkZoneHumanID(nz.Name)]
				if !exists {
					filteredNetworkZones = append(filteredNetworkZones, nz)
				}
			}

			err = addNetworkZones(client.UseProject(projectName), projectNode, dag, humanIDtoGraphID, filteredNetworkZones, nodeID)
			if err != nil {
				return err
			}
		}
	}

	// 2) Export networks
	nets, err := client.GetNetworks()
	if err != nil {
		return err
	}

	err = addNetworks(client, "default", defaultProjectNode, dag, humanIDtoGraphID, nets, nil, nodeID)
	if err != nil {
		return err
	}

	for projectName, projectNode := range otherProjectNodes {
		nets, err := client.UseProject(projectName).GetNetworks()
		if err != nil {
			return err
		}

		if shared.IsTrue(featuresPerProject[projectName]["features.networks"]) {
			// We don't inherit from 'default' project networks.
			err = addNetworks(client.UseProject(projectName), projectName, projectNode, dag, humanIDtoGraphID, nets, nil, nodeID)
			if err != nil {
				return err
			}
		} else {
			// We inherit from 'default' project networks.
			inheritedNetworks := make([]api.Network, 0)
			filteredNetworks := make([]api.Network, 0)
			for _, net := range nets {
				_, exists := humanIDtoGraphID[nodes.GenerateNetworkHumanID("default", net.Name)]
				if exists {
					inheritedNetworks = append(inheritedNetworks, net)
				} else {
					filteredNetworks = append(filteredNetworks, net)
				}
			}

			err = addNetworks(client.UseProject(projectName), projectName, projectNode, dag, humanIDtoGraphID, filteredNetworks, inheritedNetworks, nodeID)
			if err != nil {
				return err
			}
		}
	}

	// 3) Export network peers
	err = addNetworkPeers(client, "default", dag, humanIDtoGraphID, nodeID)
	if err != nil {
		return err
	}

	for projectName := range otherProjectNodes {
		err = addNetworkPeers(client.UseProject(projectName), projectName, dag, humanIDtoGraphID, nodeID)
		if err != nil {
			return err
		}
	}

	// 4) Export network ACLs
	err = addNetworkACLs(client, "default", defaultProjectNode, dag, humanIDtoGraphID, nodeID)
	if err != nil {
		return err
	}

	for projectName, projectNode := range otherProjectNodes {
		err = addNetworkACLs(client.UseProject(projectName), projectName, projectNode, dag, humanIDtoGraphID, nodeID)
		if err != nil {
			return err
		}
	}

	return nil
}

func addStorageVolumes(
	client lxd.InstanceServer,
	projectName string,
	graph *simple.DirectedGraph,
	humanIDtoGraphID map[string]int64,
	nodeID *int64,
	poolNodes []*nodes.StoragePoolNode,
	inherit bool,
) error {
	for _, poolNode := range poolNodes {
		volumes, err := client.GetStoragePoolVolumes(poolNode.Name)
		if err != nil {
			return err
		}

		for _, volume := range volumes {
			if inherit {
				// Check if the volume is already in the graph
				_, exists := humanIDtoGraphID[nodes.GenerateStorageVolumeHumanID(projectName, poolNode.Name, volume.Name, volume.Location)]
				if exists {
					continue
				}
			}

			volumeNode := nodes.NewStorageVolumeNode(projectName, poolNode.Name, volume.Name, volume.Location, volume, *nodeID)
			*nodeID++
			graph.AddNode(volumeNode)
			humanIDtoGraphID[volumeNode.HumanID()] = volumeNode.ID()
			graph.SetEdge(graph.NewEdge(poolNode, volumeNode))

			// Add the storage volume snapshots
			snapshots, err := client.GetStoragePoolVolumeSnapshots(poolNode.Name, volume.Type, volume.Name)
			if err != nil {
				return err
			}

			for _, snapshot := range snapshots {
				snapshotNode := nodes.NewStorageVolumeSnapshotNode(projectName, poolNode.Driver, poolNode.Name, volume.Name, snapshot.Name, snapshot, *nodeID)
				*nodeID++
				graph.AddNode(snapshotNode)
				humanIDtoGraphID[snapshotNode.HumanID()] = snapshotNode.ID()
				graph.SetEdge(graph.NewEdge(volumeNode, snapshotNode))
			}
		}
	}

	return nil
}

func addStorageBucketsAndKeys(
	client lxd.InstanceServer,
	projectName string,
	graph *simple.DirectedGraph,
	humanIDtoGraphID map[string]int64,
	nodeID *int64,
	poolNodes []*nodes.StoragePoolNode,
	inherit bool,
) error {
	for _, poolNode := range poolNodes {
		if !shared.ValueInSlice(poolNode.Driver, []string{"btrfs", "cephobject", "dir", "lvm", "zfs"}) {
			continue
		}

		buckets, err := client.GetStoragePoolBuckets(poolNode.Name)
		if err != nil {
			return err
		}

		for _, bucket := range buckets {
			if inherit {
				// Check if the bucket is already in the graph
				_, exists := humanIDtoGraphID[nodes.GenerateStorageBucketHumanID(projectName, poolNode.Driver, poolNode.Name, bucket.Name)]
				if exists {
					continue
				}
			}

			bucketNode := nodes.NewStorageBucketNode(projectName, poolNode.Driver, poolNode.Name, bucket.Name, bucket, *nodeID)
			*nodeID++
			graph.AddNode(bucketNode)
			humanIDtoGraphID[bucketNode.HumanID()] = bucketNode.ID()
			graph.SetEdge(graph.NewEdge(poolNode, bucketNode))

			// Add the bucket keys
			keys, err := client.GetStoragePoolBucketKeys(poolNode.Name, bucket.Name)
			if err != nil {
				return err
			}

			for _, key := range keys {
				bucketKeyNode := nodes.NewStorageBucketKeyNode(projectName, poolNode.Driver, poolNode.Name, bucket.Name, key.Name, key, *nodeID)
				*nodeID++
				graph.AddNode(bucketKeyNode)
				humanIDtoGraphID[bucketKeyNode.HumanID()] = bucketKeyNode.ID()
				graph.SetEdge(graph.NewEdge(bucketNode, bucketKeyNode))
			}
		}
	}

	return nil
}

func exportStorage(
	client lxd.InstanceServer,
	dag *simple.DirectedGraph,
	humanIDtoGraphID map[string]int64,
	rootNode *nodes.RootNode,
	otherProjectNodes map[string]*nodes.ProjectNode,
	featuresPerProject map[string]map[string]string,
	nodeID *int64,
) error {
	// 1) First, process the 'storage pools'
	storagePoolNames, err := client.GetStoragePoolNames()
	if err != nil {
		return err
	}

	poolNodes := make([]*nodes.StoragePoolNode, 0)
	for _, poolName := range storagePoolNames {
		pool, _, err := client.GetStoragePool(poolName)
		if err != nil {
			return err
		}

		poolNode := nodes.NewStoragePoolNode(pool.Name, pool, *nodeID) // A storage pool is always dependent on the root node.
		*nodeID++
		dag.AddNode(poolNode)
		humanIDtoGraphID[poolNode.HumanID()] = poolNode.ID()
		dag.SetEdge(dag.NewEdge(rootNode, poolNode))
		poolNodes = append(poolNodes, poolNode)
	}

	// 2) Then, process the 'storage volumes'
	err = addStorageVolumes(client, "default", dag, humanIDtoGraphID, nodeID, poolNodes, false)
	if err != nil {
		return err
	}

	for projectName := range otherProjectNodes {
		if shared.IsTrue(featuresPerProject[projectName]["features.storage.volumes"]) {
			err = addStorageVolumes(client.UseProject(projectName), projectName, dag, humanIDtoGraphID, nodeID, poolNodes, false)
			if err != nil {
				return err
			}
		} else {
			err = addStorageVolumes(client.UseProject(projectName), projectName, dag, humanIDtoGraphID, nodeID, poolNodes, true)
			if err != nil {
				return err
			}
		}
	}

	// 3) Export storage buckets
	err = addStorageBucketsAndKeys(client, "default", dag, humanIDtoGraphID, nodeID, poolNodes, false)
	if err != nil {
		return err
	}

	for projectName := range otherProjectNodes {
		if shared.IsTrue(featuresPerProject[projectName]["features.storage.buckets"]) {
			err = addStorageBucketsAndKeys(client.UseProject(projectName), projectName, dag, humanIDtoGraphID, nodeID, poolNodes, false)
			if err != nil {
				return err
			}
		} else {
			err = addStorageBucketsAndKeys(client.UseProject(projectName), projectName, dag, humanIDtoGraphID, nodeID, poolNodes, true)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func addDevices(
	projectName string,
	projectNode *nodes.ProjectNode,
	graph *simple.DirectedGraph,
	humanIDtoGraphID map[string]int64,
	nodeID *int64,
	devices map[string]map[string]string,
	root any,
) ([]*nodes.DeviceNode, error) {
	var profile api.Profile
	isProfile := false
	var instance api.Instance
	switch root := root.(type) {
	case api.Profile:
		profile = root
		isProfile = true
	case api.Instance:
		instance = root
	default:
		return nil, fmt.Errorf("Invalid root type %T", root)
	}

	deviceNodes := make([]*nodes.DeviceNode, 0)
	for devName, devConfig := range devices {
		devType := devConfig["type"]
		if devType == "disk" {
			source := devConfig["source"]
			if devConfig["pool"] == "" {
				// We know that 'source' is not a storage volume. The disk device is only dependent on the project node.
				var deviceNode *nodes.DeviceNode
				if isProfile {
					deviceNode = nodes.NewDeviceNode(projectName, fmt.Sprintf("profile-%s", profile.Name), devType, devName, devConfig, *nodeID)
					*nodeID++
				} else {
					deviceNode = nodes.NewDeviceNode(projectName, fmt.Sprintf("instance-%s", instance.Name), devType, devName, devConfig, *nodeID)
					*nodeID++
				}

				deviceNodes = append(deviceNodes, deviceNode)
				graph.AddNode(deviceNode)
				humanIDtoGraphID[deviceNode.HumanID()] = deviceNode.ID()
				graph.SetEdge(graph.NewEdge(projectNode, deviceNode))
			} else {
				// There is a pool but we don't know if the source is a storage volume or just a path. Let's check.
				storageVolumeIDs := make([]int64, 0)
				for humanID, id := range humanIDtoGraphID {
					if strings.HasPrefix(humanID, fmt.Sprintf("%s%s_%s_%s_", nodes.StorageVolumePrefix, projectName, devConfig["pool"], source)) {
						storageVolumeIDs = append(storageVolumeIDs, id)
					}
				}

				if len(storageVolumeIDs) > 0 {
					// The disk device is dependent on a storage volume (that can be on multiple locations).
					storageVolumeNodes := make([]*nodes.StorageVolumeNode, len(storageVolumeIDs))
					for i, storageVolumeID := range storageVolumeIDs {
						storageVolumeNode, ok := graph.Node(storageVolumeID).(*nodes.StorageVolumeNode)
						if !ok {
							return nil, fmt.Errorf("The storage volume node %q is not in the graph", devConfig["source"])
						}

						storageVolumeNodes[i] = storageVolumeNode
					}

					var deviceNode *nodes.DeviceNode
					if isProfile {
						deviceNode = nodes.NewDeviceNode(projectName, fmt.Sprintf("profile-%s", profile.Name), devType, devName, devConfig, *nodeID)
						*nodeID++
					} else {
						deviceNode = nodes.NewDeviceNode(projectName, fmt.Sprintf("instance-%s", instance.Name), devType, devName, devConfig, *nodeID)
						*nodeID++
					}

					deviceNodes = append(deviceNodes, deviceNode)
					graph.AddNode(deviceNode)
					humanIDtoGraphID[deviceNode.HumanID()] = deviceNode.ID()
					for _, storageVolumeNode := range storageVolumeNodes {
						graph.SetEdge(graph.NewEdge(storageVolumeNode, deviceNode))
						if storageVolumeNode.Project != projectName {
							// If the volume is in a different project (inherited from 'default'), we need to connect the device to the project node.
							if !graph.HasEdgeBetween(projectNode.ID(), deviceNode.ID()) {
								graph.SetEdge(graph.NewEdge(projectNode, deviceNode))
							}
						}
					}
				} else {
					// The disk device is not dependent on a storage volume, so it is dependent on the project node and the storage pool directly.
					var deviceNode *nodes.DeviceNode
					if isProfile {
						deviceNode = nodes.NewDeviceNode(projectName, fmt.Sprintf("profile-%s", profile.Name), devType, devName, devConfig, *nodeID)
						*nodeID++
					} else {
						deviceNode = nodes.NewDeviceNode(projectName, fmt.Sprintf("instance-%s", instance.Name), devType, devName, devConfig, *nodeID)
						*nodeID++
					}

					storagePoolID, ok := humanIDtoGraphID[nodes.GenerateStoragePoolHumanID(devConfig["pool"])]
					if !ok {
						return nil, fmt.Errorf("The storage pool node %q is not in the graph", devConfig["pool"])
					}

					storagePoolNode, ok := graph.Node(storagePoolID).(*nodes.StoragePoolNode)
					if !ok {
						return nil, fmt.Errorf("The node at %d  is not a storage pool", storagePoolID)
					}

					deviceNodes = append(deviceNodes, deviceNode)
					graph.AddNode(deviceNode)
					humanIDtoGraphID[deviceNode.HumanID()] = deviceNode.ID()
					graph.SetEdge(graph.NewEdge(projectNode, deviceNode))
					graph.SetEdge(graph.NewEdge(storagePoolNode, deviceNode))
				}
			}
		} else if devType == "nic" {
			// A nic device can be dependent on a network
			if devConfig["network"] != "" {
				networkNodeID, exists := humanIDtoGraphID[nodes.GenerateNetworkHumanID(projectName, devConfig["network"])]
				if !exists {
					if isProfile {
						return nil, fmt.Errorf("Profile %q nic device %q depends on a non-existing network %q", profile.Name, devName, devConfig["network"])
					} else {
						return nil, fmt.Errorf("Instance %q nic device %q depends on a non-existing network %q", instance.Name, devName, devConfig["network"])
					}
				}

				networkNode, ok := graph.Node(networkNodeID).(*nodes.NetworkNode)
				if !ok {
					return nil, fmt.Errorf("The network node %q is not in the graph", devConfig["network"])
				}

				aclNodes := make([]*nodes.NetworkACLNode, 0)
				if devConfig["security.acls"] != "" {
					acls := strings.Split(devConfig["security.acls"], ",")
					for _, acl := range acls {
						networkACLid, exists := humanIDtoGraphID[nodes.GenerateNetworkACLHumanID(projectName, acl)]
						if !exists {
							if isProfile {
								return nil, fmt.Errorf("Profile %q nic device %q depends on a non-existing network ACL %q", profile.Name, devName, acl)
							} else {
								return nil, fmt.Errorf("Instance %q nic device %q depends on a non-existing network ACL %q", instance.Name, devName, acl)
							}
						}

						networkACLNode, ok := graph.Node(networkACLid).(*nodes.NetworkACLNode)
						if !ok {
							return nil, fmt.Errorf("The network ACL node %q is not in the graph", acl)
						}

						aclNodes = append(aclNodes, networkACLNode)
					}
				}

				var deviceNode *nodes.DeviceNode
				if isProfile {
					deviceNode = nodes.NewDeviceNode(projectName, fmt.Sprintf("profile-%s", profile.Name), devType, devName, devConfig, *nodeID)
					*nodeID++
				} else {
					deviceNode = nodes.NewDeviceNode(projectName, fmt.Sprintf("instance-%s", instance.Name), devType, devName, devConfig, *nodeID)
					*nodeID++
				}

				deviceNodes = append(deviceNodes, deviceNode)
				graph.AddNode(deviceNode)
				humanIDtoGraphID[deviceNode.HumanID()] = deviceNode.ID()
				graph.SetEdge(graph.NewEdge(networkNode, deviceNode))
				if networkNode.Project != projectName {
					// If the network is in a different project (inherited from 'default'), we need to connect the device to the project node.
					graph.SetEdge(graph.NewEdge(projectNode, deviceNode))
				}

				// Attach the network ACLs to the nic device if any.
				for _, aclNode := range aclNodes {
					graph.SetEdge(graph.NewEdge(aclNode, deviceNode))
				}
			} else {
				// The nic device is not dependent on a network, so it is dependent on the project node.
				var deviceNode *nodes.DeviceNode
				if isProfile {
					deviceNode = nodes.NewDeviceNode(projectName, fmt.Sprintf("profile-%s", profile.Name), devType, devName, devConfig, *nodeID)
					*nodeID++
				} else {
					deviceNode = nodes.NewDeviceNode(projectName, fmt.Sprintf("instance-%s", instance.Name), devType, devName, devConfig, *nodeID)
					*nodeID++
				}

				deviceNodes = append(deviceNodes, deviceNode)
				graph.AddNode(deviceNode)
				humanIDtoGraphID[deviceNode.HumanID()] = deviceNode.ID()
				graph.SetEdge(graph.NewEdge(projectNode, deviceNode))
			}
		} else {
			// Connect the device to the project node.
			var deviceNode *nodes.DeviceNode
			if isProfile {
				deviceNode = nodes.NewDeviceNode(projectName, fmt.Sprintf("profile-%s", profile.Name), devType, devName, devConfig, *nodeID)
				*nodeID++
			} else {
				deviceNode = nodes.NewDeviceNode(projectName, fmt.Sprintf("instance-%s", instance.Name), devType, devName, devConfig, *nodeID)
				*nodeID++
			}

			deviceNodes = append(deviceNodes, deviceNode)
			graph.AddNode(deviceNode)
			humanIDtoGraphID[deviceNode.HumanID()] = deviceNode.ID()
			graph.SetEdge(graph.NewEdge(projectNode, deviceNode))
		}
	}

	return deviceNodes, nil
}

func addProfiles(
	projectName string,
	projectNode *nodes.ProjectNode,
	graph *simple.DirectedGraph,
	humanIDtoGraphID map[string]int64,
	profiles []api.Profile,
	nodeID *int64,
) error {
	for _, profile := range profiles {
		profileDevices := profile.Devices
		deviceNodes, err := addDevices(projectName, projectNode, graph, humanIDtoGraphID, nodeID, profileDevices, profile)
		if err != nil {
			return err
		}

		// A profile can exist without any device, in that case we connect it to the project node.
		profileNode := nodes.NewProfileNode(projectName, profile.Name, profile, *nodeID)
		*nodeID++
		graph.AddNode(profileNode)
		humanIDtoGraphID[profileNode.HumanID()] = profileNode.ID()
		if len(deviceNodes) == 0 {
			graph.SetEdge(graph.NewEdge(projectNode, profileNode))
		} else {
			for _, deviceNode := range deviceNodes {
				graph.SetEdge(graph.NewEdge(deviceNode, profileNode))
			}
		}
	}

	return nil
}

func addInstances(
	projectName string,
	projectNode *nodes.ProjectNode,
	graph *simple.DirectedGraph,
	humanIDtoGraphID map[string]int64,
	instances []api.Instance,
	inheritDefaultProfiles bool,
	nodeID *int64,
) error {
	for _, inst := range instances {
		profiles := inst.Profiles
		localDevices := inst.Devices

		// profiles to instance connections.
		profileNodes := make([]*nodes.ProfileNode, 0)
		for _, profile := range profiles {
			profileID, exists := humanIDtoGraphID[nodes.GenerateProfileHumanID(projectName, profile)]
			if !exists {
				if inheritDefaultProfiles {
					profileID, exists = humanIDtoGraphID[nodes.GenerateProfileHumanID("default", profile)]
					if !exists {
						return fmt.Errorf("Instance %q depends on a non-existing profile (even with 'features.profiles=false') %q", inst.Name, profile)
					}
				} else {
					return fmt.Errorf("Instance %q depends on a non-existing profile %q", inst.Name, profile)
				}
			}

			profileNode, ok := graph.Node(profileID).(*nodes.ProfileNode)
			if !ok {
				return fmt.Errorf("The profile node %q is not in the graph", profile)
			}

			profileNodes = append(profileNodes, profileNode)
		}

		// Create device nodes that are not part of any profile.
		localDeviceNodes, err := addDevices(projectName, projectNode, graph, humanIDtoGraphID, nodeID, localDevices, inst)
		if err != nil {
			return err
		}

		// Add the instance node and connect its dependencies (profiles and local devices if any).
		instanceNode := nodes.NewInstanceNode(projectName, inst.Name, inst, *nodeID)
		*nodeID++
		graph.AddNode(instanceNode)
		humanIDtoGraphID[instanceNode.HumanID()] = instanceNode.ID()
		for _, profileNode := range profileNodes {
			graph.SetEdge(graph.NewEdge(profileNode, instanceNode))
		}

		for _, deviceNode := range localDeviceNodes {
			graph.SetEdge(graph.NewEdge(deviceNode, instanceNode))
		}
	}

	return nil
}

func exportInstances(
	client lxd.InstanceServer,
	dag *simple.DirectedGraph,
	humanIDtoGraphID map[string]int64,
	defaultProjectNode *nodes.ProjectNode,
	otherProjectNodes map[string]*nodes.ProjectNode,
	featuresPerProject map[string]map[string]string,
	nodeID *int64,
) error {
	// 1) First, process the 'profiles'
	profiles, err := client.GetProfiles()
	if err != nil {
		return err
	}

	err = addProfiles("default", defaultProjectNode, dag, humanIDtoGraphID, profiles, nodeID)
	if err != nil {
		return err
	}

	for projectName, projectNode := range otherProjectNodes {
		profiles, err := client.UseProject(projectName).GetProfiles()
		if err != nil {
			return err
		}

		if shared.IsTrue(featuresPerProject[projectName]["features.profiles"]) {
			err = addProfiles(projectName, projectNode, dag, humanIDtoGraphID, profiles, nodeID)
			if err != nil {
				return err
			}
		} else {
			filteredProfiles := make([]api.Profile, 0)
			for _, profile := range profiles {
				_, exists := humanIDtoGraphID[nodes.GenerateProfileHumanID("default", profile.Name)]
				if !exists {
					filteredProfiles = append(filteredProfiles, profile)
				}
			}

			err = addProfiles(projectName, projectNode, dag, humanIDtoGraphID, filteredProfiles, nodeID)
			if err != nil {
				return err
			}
		}
	}

	// 2) Then, process the 'instances'
	containers, err := client.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return err
	}

	vms, err := client.GetInstances(api.InstanceTypeVM)
	if err != nil {
		return err
	}

	err = addInstances("default", defaultProjectNode, dag, humanIDtoGraphID, append(containers, vms...), false, nodeID)
	if err != nil {
		return err
	}

	for projectName, projectNode := range otherProjectNodes {
		containers, err := client.UseProject(projectName).GetInstances(api.InstanceTypeContainer)
		if err != nil {
			return err
		}

		vms, err := client.UseProject(projectName).GetInstances(api.InstanceTypeVM)
		if err != nil {
			return err
		}

		inheritDefaultProfiles := false
		if !shared.IsTrue(featuresPerProject[projectName]["features.profiles"]) {
			inheritDefaultProfiles = true
		}

		err = addInstances(projectName, projectNode, dag, humanIDtoGraphID, append(containers, vms...), inheritDefaultProfiles, nodeID)
		if err != nil {
			return err
		}
	}

	return nil
}

func ExportClusterDAG(client lxd.InstanceServer, logger *logger.SafeLogger) (*simple.DirectedGraph, map[string]int64, error) {
	dag := simple.NewDirectedGraph()
	humanIDtoGraphID := make(map[string]int64)

	nodeID := int64(0)
	// Add root node
	rootNode, err := nodes.NewRootNode(client, nodeID)
	if err != nil {
		return nil, nil, err
	}

	nodeID++
	dag.AddNode(rootNode)
	humanIDtoGraphID[rootNode.HumanID()] = rootNode.ID()

	// Get default project
	projects, err := client.GetProjects()
	if err != nil {
		return nil, nil, err
	}

	if len(projects) == 0 {
		return nil, nil, errors.New("No project found")
	}

	var defaultProjectNode *nodes.ProjectNode
	otherProjectNodes := make(map[string]*nodes.ProjectNode)
	if len(projects) == 1 {
		// Only default project
		defaultProjectNode = nodes.NewProjectNode(projects[0].Name, projects[0], nodeID)
		dag.AddNode(defaultProjectNode)
		humanIDtoGraphID[defaultProjectNode.HumanID()] = defaultProjectNode.ID()
		dag.SetEdge(dag.NewEdge(rootNode, defaultProjectNode))
		nodeID++
	} else {
		for _, project := range projects {
			projectNode := nodes.NewProjectNode(project.Name, project, nodeID)
			dag.AddNode(projectNode)
			humanIDtoGraphID[projectNode.HumanID()] = projectNode.ID()
			if project.Name == "default" {
				defaultProjectNode = projectNode
				dag.SetEdge(dag.NewEdge(rootNode, projectNode))
			} else {
				otherProjectNodes[project.Name] = projectNode
			}

			nodeID++
		}

		for _, otherProjectNode := range otherProjectNodes {
			dag.SetEdge(dag.NewEdge(rootNode, otherProjectNode))
		}
	}

	features := getFeaturesPerProject(projects)
	err = exportNetworking(client, dag, humanIDtoGraphID, defaultProjectNode, otherProjectNodes, features, &nodeID)
	if err != nil {
		return nil, nil, err
	}

	err = exportStorage(client, dag, humanIDtoGraphID, rootNode, otherProjectNodes, features, &nodeID)
	if err != nil {
		return nil, nil, err
	}

	err = exportInstances(client, dag, humanIDtoGraphID, defaultProjectNode, otherProjectNodes, features, &nodeID)
	if err != nil {
		return nil, nil, err
	}

	return dag, humanIDtoGraphID, nil
}
