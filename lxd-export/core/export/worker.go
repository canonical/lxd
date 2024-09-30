package export

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd-export/core/logger"
	"github.com/canonical/lxd/lxd-export/core/nodes"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/sirupsen/logrus"
	"gonum.org/v1/gonum/graph/simple"
)

type DAGWorker struct {
	// worker (local logic)
	client lxd.InstanceServer
	logger *logger.SafeLogger
	id     string

	// worker (broadcasting logic for inter-worker communication)
	broadcaster  *Broadcaster
	incoming     chan Message
	totalWorkers uint

	// graph
	globalRoot        *nodes.RootNode
	localRoot         *nodes.ProjectNode
	rootFeatures      map[string]string
	graph             *simple.DirectedGraph
	muGraph           *sync.RWMutex
	humanIDtoNodeID   map[string]int64
	muHumanIDtoNodeID *sync.RWMutex

	// rank
	rootRank uint
}

func NewDAGWorker(
	client lxd.InstanceServer,
	logger *logger.SafeLogger,
	id string,
	broadcaster *Broadcaster,
	totalWorkers uint,
	globalRoot *nodes.RootNode,
	localRoot *nodes.ProjectNode,
	rootFeatures map[string]string,
	g *simple.DirectedGraph,
	muGraph *sync.RWMutex,
	humanIDtoNodeID map[string]int64,
	muHumanIDtoNodeID *sync.RWMutex,
	rootRank uint,
) *DAGWorker {
	var incoming chan Message
	if broadcaster != nil {
		incoming = broadcaster.Register(id)
	} else {
		incoming = nil
	}

	return &DAGWorker{
		client:            client,
		logger:            logger,
		id:                id,
		broadcaster:       broadcaster,
		incoming:          incoming,
		totalWorkers:      totalWorkers,
		globalRoot:        globalRoot,
		localRoot:         localRoot,
		rootFeatures:      rootFeatures,
		graph:             g,
		muGraph:           muGraph,
		humanIDtoNodeID:   humanIDtoNodeID,
		muHumanIDtoNodeID: muHumanIDtoNodeID,
		rootRank:          rootRank,
	}
}

func (d *DAGWorker) addNetworkZones(nzs []api.NetworkZone) error {
	d.muGraph.Lock()
	d.muHumanIDtoNodeID.Lock()
	for _, nz := range nzs {
		nzNode := nodes.NewNetworkZoneNode(nz.Name, nz, d.rootRank+1)

		d.graph.AddNode(nzNode)
		d.graph.SetEdge(d.graph.NewEdge(d.localRoot, nzNode))
		d.humanIDtoNodeID[nzNode.HumanID()] = nzNode.ID()

		// Add network zone records (only dependent on their network zone)
		records, err := d.client.GetNetworkZoneRecords(nz.Name)
		if err != nil {
			d.muGraph.Unlock()
			d.muHumanIDtoNodeID.Unlock()
			return err
		}

		for _, record := range records {
			recordNode := nodes.NewNetworkZoneRecordNode(nz.Name, record.Name, record, nzNode.Rank()+1)
			d.graph.AddNode(recordNode)
			d.graph.SetEdge(d.graph.NewEdge(nzNode, recordNode))
			d.humanIDtoNodeID[recordNode.HumanID()] = recordNode.ID()
		}
	}

	d.muGraph.Unlock()
	d.muHumanIDtoNodeID.Unlock()
	return nil
}

func (d *DAGWorker) addNetworkLBs(netNode *nodes.NetworkNode) error {
	lbs, err := d.client.GetNetworkLoadBalancers(netNode.Name)
	if err != nil {
		return err
	}

	for _, lb := range lbs {
		lbNode := nodes.NewNetworkLBNode(d.id, netNode.Name, lb, netNode.Rank()+1)
		d.muHumanIDtoNodeID.Lock()
		_, exists := d.humanIDtoNodeID[lbNode.HumanID()]
		if exists {
			d.muHumanIDtoNodeID.Unlock()
			continue
		}

		d.humanIDtoNodeID[lbNode.HumanID()] = lbNode.ID()
		d.muHumanIDtoNodeID.Unlock()

		d.muGraph.Lock()
		d.graph.AddNode(lbNode)
		d.graph.SetEdge(d.graph.NewEdge(netNode, lbNode))
		d.muGraph.Unlock()
	}

	return nil
}

func (d *DAGWorker) addNetworkForwards(netNode *nodes.NetworkNode) error {
	forwards, err := d.client.GetNetworkForwards(netNode.Name)
	if err != nil {
		return err
	}

	for _, forward := range forwards {
		forwardNode := nodes.NewNetworkForwardNode(d.id, netNode.Name, forward, netNode.Rank()+1)
		d.muHumanIDtoNodeID.Lock()
		_, exists := d.humanIDtoNodeID[forwardNode.HumanID()]
		if exists {
			d.muHumanIDtoNodeID.Unlock()
			continue
		}

		d.humanIDtoNodeID[forwardNode.HumanID()] = forwardNode.ID()
		d.muHumanIDtoNodeID.Unlock()

		d.muGraph.Lock()
		d.graph.AddNode(forwardNode)
		d.graph.SetEdge(d.graph.NewEdge(netNode, forwardNode))
		d.muGraph.Unlock()
	}

	return nil
}

func (d *DAGWorker) addNetworks(networks []api.Network, inheritedNetworks []api.Network) error {
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
		} else {
			d.logger.Warn("Invalid network type. Ignoring.", logrus.Fields{"id": d.id, "type": net.Type})
		}
	}

	// Find nodes with no incoming edges (no parents)
	var queue []string
	for id, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}

	currentLocalRank := uint(0)

	// BFS
	for len(queue) > 0 {
		levelSize := len(queue)
		for i := 0; i < levelSize; i++ {
			node := queue[0]
			queue = queue[1:]

			net := netMap[node]
			netRank := d.rootRank + currentLocalRank

			// Add to DAG
			// Check if the network is dependent on a network zone
			var nzFound bool
			var nzNode *nodes.NetworkZoneNode
			for _, nz := range []string{"dns.zone.forward", "dns.zone.reverse.ipv4", "dns.zone.reverse.ipv6"} {
				if net.Config[nz] != "" {
					d.muHumanIDtoNodeID.Lock()
					nzID, exists := d.humanIDtoNodeID[nodes.GenerateNetworkZoneHumanID(net.Config["dns.zone.forward"])]
					if !exists {
						d.muHumanIDtoNodeID.Unlock()
						return fmt.Errorf("Network %q depends on a non-existing network zone %q", net.Name, net.Config["dns.zone.forward"])
					}

					d.muHumanIDtoNodeID.Unlock()
					d.muGraph.RLock()
					nzNode, nzFound = d.graph.Node(nzID).(*nodes.NetworkZoneNode)
					if !nzFound {
						return errors.New("Invalid node type for network zone")
					}

					d.muGraph.RUnlock()
					nzRank := nzNode.Rank()
					if netRank < nzRank {
						netRank = nzRank
					}

					break
				}
			}

			var parentNetworkNode *nodes.NetworkNode
			if currentLocalRank != 0 {
				parentNetwork := parents[node]
				var parentNetworkProject string
				if d.id != "default" {
					if inheritedNetworks == nil {
						parentNetworkProject = d.id
					} else {
						for _, net := range inheritedNetworks {
							if net.Name == parentNetwork {
								parentNetworkProject = "default"
								break
							}
						}

						if parentNetworkProject == "" {
							parentNetworkProject = d.id
						}
					}
				} else {
					parentNetworkProject = "default"
				}

				d.muHumanIDtoNodeID.Lock()
				parentID, exists := d.humanIDtoNodeID[nodes.GenerateNetworkHumanID(parentNetworkProject, parentNetwork)]
				if !exists {
					d.muHumanIDtoNodeID.Unlock()
					return fmt.Errorf("Network %q depends on a non-existing parent network %q", net.Name, parentNetwork)
				}

				d.muHumanIDtoNodeID.Unlock()

				d.muGraph.RLock()
				var ok bool
				parentNetworkNode, ok = d.graph.Node(parentID).(*nodes.NetworkNode)
				if !ok {
					d.muGraph.RUnlock()
					return fmt.Errorf("The parent network node %q is not in the graph", parentNetwork)
				}

				d.muGraph.RUnlock()
				if nzFound && nzNode != nil {
					if parentNetworkNode.Rank() > nzNode.Rank() {
						netRank = parentNetworkNode.Rank()
					}
				}
			}

			netNode := nodes.NewNetworkNode(d.id, net.Name, net, netRank+1)

			d.muGraph.Lock()
			d.muHumanIDtoNodeID.Lock()
			d.graph.AddNode(netNode)
			d.humanIDtoNodeID[netNode.HumanID()] = netNode.ID()
			if nzFound && nzNode != nil {
				d.graph.SetEdge(d.graph.NewEdge(nzNode, netNode))
			}

			if currentLocalRank == 0 {
				d.graph.SetEdge(d.graph.NewEdge(d.localRoot, netNode))
			} else {
				// It means that the network is dependent on a parent network
				if parentNetworkNode == nil {
					d.muGraph.Unlock()
					d.muHumanIDtoNodeID.Unlock()
					return errors.New("Invalid parent network node")
				}

				d.graph.SetEdge(d.graph.NewEdge(parentNetworkNode, netNode))
			}

			d.muGraph.Unlock()
			d.muHumanIDtoNodeID.Unlock()

			if net.Managed {
				if net.Type == "ovn" {
					// Attach network load balancer dependencies to the network if any.
					err := d.addNetworkLBs(netNode)
					if err != nil {
						return err
					}
				}

				if net.Type == "ovn" || net.Type == "bridge" {
					// Attach network forward dependencies to network if any.
					err := d.addNetworkForwards(netNode)
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

		currentLocalRank++
	}

	return nil
}

func (d *DAGWorker) addNetworkPeers() error {
	// Get cached network nodes for all projects. This has the advantage of working on a local
	// copy of `humanIDtoNodeID` that is shared between all the workers and to minimize lock/unlock.
	networkIDs := make(map[string]int64)
	d.muHumanIDtoNodeID.RLock()
	for humanID, id := range d.humanIDtoNodeID {
		if strings.HasPrefix(humanID, nodes.NetworkPrefix) {
			networkIDs[humanID] = id
		}
	}

	d.muHumanIDtoNodeID.RUnlock()

	networksPrefixPerProject := fmt.Sprintf("%s%s_", nodes.NetworkPrefix, d.id)
	for humanID, id := range networkIDs {
		if !strings.HasPrefix(humanID, networksPrefixPerProject) {
			continue
		}

		// Get the source network node
		d.muGraph.RLock()
		netNode, ok := d.graph.Node(id).(*nodes.NetworkNode)
		if !ok {
			d.muGraph.RUnlock()
			return fmt.Errorf("The network node %q is not in the graph", humanID)
		}

		d.muGraph.RUnlock()
		netData, ok := netNode.Data().(api.Network)
		if !ok {
			return fmt.Errorf("Invalid network data type")
		}

		// Only managed 'ovn' networks support peering.
		if netData.Managed && netData.Type == "ovn" {
			// Check if it has peers
			peers, err := d.client.GetNetworkPeers(netNode.Name)
			if err != nil {
				return err
			}

			for _, peer := range peers {
				// Check target network and project
				var targetNetworkNode *nodes.NetworkNode
				targetNetworkHumanID := nodes.GenerateNetworkHumanID(peer.TargetProject, peer.TargetNetwork)
				for targetHumanID, targetID := range networkIDs {
					if targetHumanID == targetNetworkHumanID {
						d.muGraph.RLock()
						targetNetworkNode, ok = d.graph.Node(targetID).(*nodes.NetworkNode)
						if !ok {
							d.muGraph.RUnlock()
							return fmt.Errorf("The target network node %q for the peer %q is not in the graph", targetHumanID, peer.Name)
						}

						d.muGraph.RUnlock()
						break
					}
				}

				if targetNetworkNode == nil {
					return fmt.Errorf("No target network could be found for the peer %s", peer.Name)
				}

				// Because a peer depends on two networks, we need to find the max rank between
				// the two networks before adding the network peer node to the graph.
				maxRank := netNode.Rank()
				if targetNetworkNode.Rank() > maxRank {
					maxRank = targetNetworkNode.Rank()
				}

				netPeerNode := nodes.NewNetworkPeerNode(d.id, netNode.Name, peer.Name, peer, maxRank+1)

				d.muGraph.Lock()
				d.graph.AddNode(netPeerNode)

				d.muHumanIDtoNodeID.Lock()
				d.humanIDtoNodeID[nodes.GenerateNetworkPeerHumanID(d.id, netNode.Name, peer.Name)] = netPeerNode.ID()
				d.muHumanIDtoNodeID.Unlock()

				d.graph.SetEdge(d.graph.NewEdge(netNode, netPeerNode))
				d.graph.SetEdge(d.graph.NewEdge(targetNetworkNode, netPeerNode))
				d.muGraph.Unlock()
			}
		}
	}

	return nil
}

func (d *DAGWorker) addNetworkACLs() error {
	acls, err := d.client.GetNetworkACLs()
	if err != nil {
		return err
	}

	if len(acls) == 0 {
		return nil
	}

	// Get a local copy of the peer node references,
	// as an ACL can be dependant on a network peer if an ACL rule
	// uses a network subject selector
	d.muHumanIDtoNodeID.RLock()
	projectNetworkPeerPrefix := fmt.Sprintf("%s%s_", nodes.NetworkPeerPrefix, d.id)
	netPeers := make(map[string]int64)
	for humanID, id := range d.humanIDtoNodeID {
		if strings.HasPrefix(humanID, projectNetworkPeerPrefix) {
			netPeers[humanID] = id
		}
	}

	d.muHumanIDtoNodeID.RUnlock()

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
				peerID, exists := netPeers[nodes.GenerateNetworkPeerHumanID(d.id, networkName, peerName)]
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

				peerID, exists := netPeers[nodes.GenerateNetworkPeerHumanID(d.id, networkName, peerName)]
				if !exists {
					return fmt.Errorf("Could not find the network peer (%s/%s) node reference for ACL egress destination rule %q", networkName, peerName, acl.Name)
				}

				peerIDs = append(peerIDs, peerID)
			}
		}

		d.muGraph.Lock()
		d.muHumanIDtoNodeID.Lock()
		if len(peerIDs) == 0 {
			// In this case, if an ACL rules are not dependant on any network peers,
			// then it is dependent of the root of this worker (the 'project' node).
			aclNode := nodes.NewNetworkACLNode(d.id, acl.Name, acl, d.rootRank+1)
			d.graph.AddNode(aclNode)
			d.graph.SetEdge(d.graph.NewEdge(d.localRoot, aclNode))
			d.humanIDtoNodeID[aclNode.HumanID()] = aclNode.ID()
		} else if len(peerIDs) == 1 {
			// The rank is just the rank of the network peer node + 1.
			peerNode, ok := d.graph.Node(peerIDs[0]).(*nodes.NetworkPeerNode)
			if !ok {
				d.muGraph.Unlock()
				d.muHumanIDtoNodeID.Unlock()
				return fmt.Errorf("Invalid node type for network peer")
			}

			aclNode := nodes.NewNetworkACLNode(d.id, acl.Name, acl, peerNode.Rank()+1)
			d.graph.AddNode(aclNode)
			d.graph.SetEdge(d.graph.NewEdge(peerNode, aclNode))
			d.humanIDtoNodeID[aclNode.HumanID()] = aclNode.ID()
		} else {
			// We need to find the max rank among all the network peer nodes,
			// create the network ACL node with the incremented max rank and
			// connect all the network peer nodes to the network ACL node.
			maxRank := uint(0)
			peerNodes := make([]*nodes.NetworkPeerNode, 0)
			for _, id := range peerIDs {
				peerNode, ok := d.graph.Node(id).(*nodes.NetworkPeerNode)
				if !ok {
					d.muGraph.Unlock()
					d.muHumanIDtoNodeID.Unlock()
					return fmt.Errorf("Invalid node type for network peer")
				}

				peerNodes = append(peerNodes, peerNode)
				if peerNode.Rank() > maxRank {
					maxRank = peerNode.Rank()
				}
			}

			aclNode := nodes.NewNetworkACLNode(d.id, acl.Name, acl, maxRank+1)
			d.graph.AddNode(aclNode)
			for _, peerNode := range peerNodes {
				d.graph.SetEdge(d.graph.NewEdge(peerNode, aclNode))
			}
		}

		d.muGraph.Unlock()
		d.muHumanIDtoNodeID.Unlock()
	}

	return nil
}

func (d *DAGWorker) exportNetworking() error {
	// 0) On different networking resources,
	// we need to have a barrier to wait for the 'default' project to finish its
	// networking resources before we can start processing the networking resources of the other projects.
	waitDefaultNZ := make(chan struct{})
	waitDefaultNets := make(chan struct{})
	otherNets := make(map[string]bool)
	waitOtherNets := make(chan struct{})
	waitAllNets := make(chan struct{})
	go func() {
		for msg := range d.incoming {
			switch msg.Content {
			case "DEFAULT_NETWORK_ZONES":
				close(waitDefaultNZ)
			case "DEFAULT_NETWORKS":
				close(waitDefaultNets)
			case "OTHER_NETWORKS":
				if d.id == "default" {
					otherNets[msg.SenderID] = true
					if len(otherNets) == int(d.totalWorkers-1) {
						close(waitOtherNets)
					}
				}
			case "ALL_NETWORKS":
				if d.id == "default" {
					close(waitAllNets)
				}
			}
		}

		close(waitDefaultNZ)
		close(waitDefaultNets)
		close(waitOtherNets)
		close(waitAllNets)
	}()

	if d.id != "default" {
		<-waitDefaultNZ
	}

	// 1) First, process 'network zone' because it can have a 'network' child (see: https://documentation.ubuntu.com/lxd/en/latest/howto/network_zones/#add-a-network-zone-to-a-network)
	nzs, err := d.client.GetNetworkZones()
	if err != nil {
		return err
	}

	if d.id != "default" {
		// If we get there, it means that the DEFAULT_NETWORK_ZONES and DEFAULT_NETWORKS barriers have been passed and that
		// the 'network zones' and the 'networks' from the 'default' project are already in the graph.
		if shared.IsTrue(d.rootFeatures["features.networks.zones"]) {
			// It means I don't inherit from 'default' project network zones
			// and that I can safely add the network zones to the graph as they are not already there.
			err = d.addNetworkZones(nzs)
			if err != nil {
				return err
			}
		} else {
			// it means I inherit from 'default' project network zones
			// and that some of the network zones we get there are already in the graph.
			// We need to have a filter to only add the network zones that are not already in the graph.
			filteredNetworkZones := make([]api.NetworkZone, 0)
			d.muHumanIDtoNodeID.Lock()
			for _, nz := range nzs {
				_, exists := d.humanIDtoNodeID[nodes.GenerateNetworkZoneHumanID(nz.Name)]
				if !exists {
					filteredNetworkZones = append(filteredNetworkZones, nz)
				}
			}

			d.muHumanIDtoNodeID.Unlock()
			err = d.addNetworkZones(filteredNetworkZones)
			if err != nil {
				return err
			}
		}
	} else {
		err = d.addNetworkZones(nzs)
		if err != nil {
			return err
		}

		if d.broadcaster != nil {
			d.broadcaster.Broadcast(Message{SenderID: d.id, Content: "DEFAULT_NETWORK_ZONES"})
			d.logger.Info("DEFAULT_NETWORK_ZONES signal broadcasted", logrus.Fields{"id": d.id})
		}
	}

	d.logger.Info("Local network zones added.", logrus.Fields{"id": d.id})

	// 2) Then process the 'network'
	if d.id != "default" {
		<-waitDefaultNets
	}

	nets, err := d.client.GetNetworks()
	if err != nil {
		return err
	}

	if d.id != "default" {
		if shared.IsTrue(d.rootFeatures["features.networks"]) {
			// We don't inherit from 'default' project networks.
			err = d.addNetworks(nets, nil)
			if err != nil {
				return err
			}
		} else {
			// We inherit from 'default' project networks.
			inheritedNetworks := make([]api.Network, 0)
			d.muHumanIDtoNodeID.Lock()
			for _, net := range nets {
				_, exists := d.humanIDtoNodeID[nodes.GenerateNetworkHumanID("default", net.Name)]
				if exists {
					inheritedNetworks = append(inheritedNetworks, net)
				}
			}

			d.muHumanIDtoNodeID.Unlock()
			err = d.addNetworks(nets, inheritedNetworks)
			if err != nil {
				return err
			}
		}

		d.broadcaster.Broadcast(Message{SenderID: d.id, Content: "OTHER_NETWORKS"})
		if d.totalWorkers > 2 {
			<-waitAllNets
		}
	} else {
		err = d.addNetworks(nets, nil)
		if err != nil {
			return err
		}

		if d.broadcaster != nil {
			d.broadcaster.Broadcast(Message{SenderID: d.id, Content: "DEFAULT_NETWORKS"})
			d.logger.Info("DEFAULT_NETWORKS signal broadcasted", logrus.Fields{"id": d.id})
			d.logger.Info("Waiting for other networks to be processed.", logrus.Fields{"id": d.id})
			<-waitOtherNets
			d.broadcaster.Broadcast(Message{SenderID: d.id, Content: "ALL_NETWORKS"})
			d.logger.Info("ALL_NETWORKS signal broadcasted", logrus.Fields{"id": d.id})
		}
	}

	d.logger.Info("Local network added.", logrus.Fields{"id": d.id})

	// 3) Process the 'network peers' now that all the networks have been processed in all the workers.
	err = d.addNetworkPeers()
	if err != nil {
		return err
	}

	d.logger.Info("Local network peers added.", logrus.Fields{"id": d.id})

	// 4) Process the network ACLs (that are potentially dependent on this project's network peers)
	err = d.addNetworkACLs()
	if err != nil {
		return err
	}

	d.logger.Info("Local network ACLs added.", logrus.Fields{"id": d.id})

	// No more networking elements to process
	if d.broadcaster != nil {
		d.broadcaster.Broadcast(Message{SenderID: d.id, Content: "GLOBAL_NETWORKING"})
		d.logger.Info("GLOBAL_NETWORKING signal broadcasted", logrus.Fields{"id": d.id})
	}

	return nil
}

func (d *DAGWorker) getStoragePoolNodes() ([]*nodes.StoragePoolNode, error) {
	poolNodes := make([]*nodes.StoragePoolNode, 0)
	d.muHumanIDtoNodeID.RLock()
	for humanID, id := range d.humanIDtoNodeID {
		if strings.HasPrefix(humanID, nodes.StoragePoolPrefix) {
			poolNode, ok := d.graph.Node(id).(*nodes.StoragePoolNode)
			if !ok {
				d.muHumanIDtoNodeID.RUnlock()
				return nil, fmt.Errorf("Invalid node type for storage pool")
			}

			poolNodes = append(poolNodes, poolNode)
		}
	}

	d.muHumanIDtoNodeID.RUnlock()
	return poolNodes, nil
}

func (d *DAGWorker) addStorageVolumes(poolNodes []*nodes.StoragePoolNode, inherit bool) error {
	for _, poolNode := range poolNodes {
		volumes, err := d.client.GetStoragePoolVolumes(poolNode.Name)
		if err != nil {
			return err
		}

		for _, volume := range volumes {
			if inherit {
				// Check if the volume is already in the graph
				d.muHumanIDtoNodeID.RLock()
				_, exists := d.humanIDtoNodeID[nodes.GenerateStorageVolumeHumanID(d.id, poolNode.Name, volume.Name, volume.Location)]
				d.muHumanIDtoNodeID.RUnlock()
				if exists {
					continue
				}
			}

			volumeNode := nodes.NewStorageVolumeNode(d.id, poolNode.Name, volume.Name, volume.Location, volume, poolNode.Rank()+1)
			d.muGraph.Lock()
			d.muHumanIDtoNodeID.Lock()
			d.graph.AddNode(volumeNode)
			d.humanIDtoNodeID[volumeNode.HumanID()] = volumeNode.ID()
			d.graph.SetEdge(d.graph.NewEdge(poolNode, volumeNode))
			d.muGraph.Unlock()
			d.muHumanIDtoNodeID.Unlock()

			// Add the storage volume snapshots
			snapshots, err := d.client.GetStoragePoolVolumeSnapshots(poolNode.Name, volume.Type, volume.Name)
			if err != nil {
				return err
			}

			for _, snapshot := range snapshots {
				snapshotNode := nodes.NewStorageVolumeSnapshotNode(d.id, poolNode.Driver, poolNode.Name, volume.Name, snapshot.Name, snapshot, volumeNode.Rank()+1)
				d.muGraph.Lock()
				d.muHumanIDtoNodeID.Lock()
				d.graph.AddNode(snapshotNode)
				d.humanIDtoNodeID[snapshotNode.HumanID()] = snapshotNode.ID()
				d.graph.SetEdge(d.graph.NewEdge(volumeNode, snapshotNode))
				d.muGraph.Unlock()
				d.muHumanIDtoNodeID.Unlock()
			}
		}
	}

	return nil
}

func (d *DAGWorker) addStorageBucketsAndKeys(poolNodes []*nodes.StoragePoolNode, inherit bool) error {
	for _, poolNode := range poolNodes {
		if !shared.ValueInSlice(poolNode.Driver, []string{"btrfs", "cephobject", "dir", "lvm", "zfs"}) {
			continue
		}

		buckets, err := d.client.GetStoragePoolBuckets(poolNode.Name)
		if err != nil {
			return err
		}

		for _, bucket := range buckets {
			if inherit {
				// Check if the bucket is already in the graph
				d.muHumanIDtoNodeID.RLock()
				_, exists := d.humanIDtoNodeID[nodes.GenerateStorageBucketHumanID(d.id, poolNode.Driver, poolNode.Name, bucket.Name)]
				d.muHumanIDtoNodeID.RUnlock()
				if exists {
					continue
				}
			}

			bucketNode := nodes.NewStorageBucketNode(d.id, poolNode.Driver, poolNode.Name, bucket.Name, bucket, poolNode.Rank()+1)
			d.muGraph.Lock()
			d.muHumanIDtoNodeID.Lock()
			d.graph.AddNode(bucketNode)
			d.humanIDtoNodeID[bucketNode.HumanID()] = bucketNode.ID()
			d.graph.SetEdge(d.graph.NewEdge(poolNode, bucketNode))
			d.muGraph.Unlock()
			d.muHumanIDtoNodeID.Unlock()

			// Add the bucket keys
			keys, err := d.client.GetStoragePoolBucketKeys(poolNode.Name, bucket.Name)
			if err != nil {
				return err
			}

			for _, key := range keys {
				bucketKeyNode := nodes.NewStorageBucketKeyNode(d.id, poolNode.Driver, poolNode.Name, bucket.Name, key.Name, key, bucketNode.Rank()+1)
				d.muGraph.Lock()
				d.muHumanIDtoNodeID.Lock()
				d.graph.AddNode(bucketKeyNode)
				d.humanIDtoNodeID[bucketKeyNode.HumanID()] = bucketKeyNode.ID()
				d.graph.SetEdge(d.graph.NewEdge(bucketNode, bucketKeyNode))
				d.muGraph.Unlock()
				d.muHumanIDtoNodeID.Unlock()
			}
		}
	}

	return nil
}

func (d *DAGWorker) exportStorage() error {
	waitDefaultStorageVolumes := make(chan struct{})
	waitDefaultStorageBuckets := make(chan struct{})
	go func() {
		for msg := range d.incoming {
			switch msg.Content {
			case "DEFAULT_STORAGE_VOLUMES":
				close(waitDefaultStorageVolumes)
			case "DEFAULT_STORAGE_BUCKETS":
				close(waitDefaultStorageBuckets)
			}
		}

		close(waitDefaultStorageVolumes)
		close(waitDefaultStorageBuckets)
	}()

	// 1) First, process the 'storage pools' (only in 'default' worker) and the 'storage volumes'
	if d.id != "default" {
		<-waitDefaultStorageVolumes

		poolNodes, err := d.getStoragePoolNodes()
		if err != nil {
			return err
		}

		if shared.IsTrue(d.rootFeatures["features.storage.volumes"]) {
			err := d.addStorageVolumes(poolNodes, false)
			if err != nil {
				return err
			}
		} else {
			err := d.addStorageVolumes(poolNodes, true)
			if err != nil {
				return err
			}
		}
	} else {
		// Storage pools are dependent of the root node of the DAG (not related to projects),
		// so we process them first and only in the 'default' worker.
		storagePoolNames, err := d.client.GetStoragePoolNames()
		if err != nil {
			return err
		}

		poolNodes := make([]*nodes.StoragePoolNode, 0)
		for _, poolName := range storagePoolNames {
			pool, _, err := d.client.GetStoragePool(poolName)
			if err != nil {
				return err
			}

			poolNode := nodes.NewStoragePoolNode(pool.Name, pool, d.rootRank+1) // A storage pool is always dependent on the root node.
			d.muGraph.Lock()
			d.muHumanIDtoNodeID.Lock()
			d.graph.AddNode(poolNode)
			d.humanIDtoNodeID[poolNode.HumanID()] = poolNode.ID()
			d.graph.SetEdge(d.graph.NewEdge(d.globalRoot, poolNode))
			d.muGraph.Unlock()
			d.muHumanIDtoNodeID.Unlock()
			poolNodes = append(poolNodes, poolNode)
		}

		d.logger.Info("Storage pools added.", logrus.Fields{"id": d.id})
		err = d.addStorageVolumes(poolNodes, false)
		if err != nil {
			return err
		}

		d.logger.Info("Local storage volumes added.", logrus.Fields{"id": d.id})
		if d.broadcaster != nil {
			d.broadcaster.Broadcast(Message{SenderID: d.id, Content: "DEFAULT_STORAGE_VOLUMES"})
		}
	}

	// 2) Then process the 'storage buckets'
	if d.id != "default" {
		<-waitDefaultStorageBuckets

		poolNodes, err := d.getStoragePoolNodes()
		if err != nil {
			return err
		}

		if shared.IsTrue(d.rootFeatures["features.storage.buckets"]) {
			err := d.addStorageBucketsAndKeys(poolNodes, false)
			if err != nil {
				return err
			}
		} else {
			err := d.addStorageBucketsAndKeys(poolNodes, true)
			if err != nil {
				return err
			}
		}
	} else {
		poolNodes, err := d.getStoragePoolNodes()
		if err != nil {
			return err
		}

		err = d.addStorageBucketsAndKeys(poolNodes, false)
		if err != nil {
			return err
		}

		d.logger.Info("Local storage buckets added.", logrus.Fields{"id": d.id})
		if d.broadcaster != nil {
			d.broadcaster.Broadcast(Message{SenderID: d.id, Content: "DEFAULT_STORAGE_BUCKETS"})
		}
	}

	if d.broadcaster != nil {
		d.broadcaster.Broadcast(Message{SenderID: d.id, Content: "GLOBAL_STORAGE"})
	}

	return nil
}

func (d *DAGWorker) addDevices(devices map[string]map[string]string, root any) ([]*nodes.DeviceNode, error) {
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
				d.muGraph.Lock()
				d.muHumanIDtoNodeID.Lock()
				rank := d.localRoot.Rank() + 1
				var deviceNode *nodes.DeviceNode
				if isProfile {
					deviceNode = nodes.NewDeviceNode(d.id, fmt.Sprintf("profile-%s", profile.Name), devType, devName, devConfig, rank)
				} else {
					deviceNode = nodes.NewDeviceNode(d.id, fmt.Sprintf("instance-%s", instance.Name), devType, devName, devConfig, rank)
				}

				deviceNodes = append(deviceNodes, deviceNode)
				d.graph.AddNode(deviceNode)
				d.humanIDtoNodeID[deviceNode.HumanID()] = deviceNode.ID()
				d.graph.SetEdge(d.graph.NewEdge(d.localRoot, deviceNode))
				d.muGraph.Unlock()
				d.muHumanIDtoNodeID.Unlock()
			} else {
				// There is a pool but we don't know if the source is a storage volume or just a path. Let's check.
				d.muHumanIDtoNodeID.RLock()
				storageVolumeIDs := make([]int64, 0)
				for humanID, id := range d.humanIDtoNodeID {
					if strings.HasPrefix(humanID, fmt.Sprintf("%s%s_%s_%s_", nodes.StorageVolumePrefix, d.id, devConfig["pool"], source)) {
						storageVolumeIDs = append(storageVolumeIDs, id)
					}
				}

				d.muHumanIDtoNodeID.RUnlock()
				if len(storageVolumeIDs) > 0 {
					// The disk device is dependent on a storage volume (that can be on multiple locations).
					storageVolumeNodes := make([]*nodes.StorageVolumeNode, len(storageVolumeIDs))
					maxVolumeNodeRank := uint(0)
					for i, storageVolumeID := range storageVolumeIDs {
						d.muGraph.RLock()
						storageVolumeNode, ok := d.graph.Node(storageVolumeID).(*nodes.StorageVolumeNode)
						if !ok {
							d.muGraph.RUnlock()
							return nil, fmt.Errorf("The storage volume node %q is not in the graph", devConfig["source"])
						}

						d.muGraph.RUnlock()
						if storageVolumeNode.Rank() > maxVolumeNodeRank {
							maxVolumeNodeRank = storageVolumeNode.Rank()
						}

						storageVolumeNodes[i] = storageVolumeNode
					}

					d.muGraph.Lock()
					d.muHumanIDtoNodeID.Lock()
					var deviceNode *nodes.DeviceNode
					if isProfile {
						deviceNode = nodes.NewDeviceNode(d.id, fmt.Sprintf("profile-%s", profile.Name), devType, devName, devConfig, maxVolumeNodeRank+1)
					} else {
						deviceNode = nodes.NewDeviceNode(d.id, fmt.Sprintf("instance-%s", instance.Name), devType, devName, devConfig, maxVolumeNodeRank+1)
					}

					deviceNodes = append(deviceNodes, deviceNode)
					d.graph.AddNode(deviceNode)
					d.humanIDtoNodeID[deviceNode.HumanID()] = deviceNode.ID()
					for _, storageVolumeNode := range storageVolumeNodes {
						d.graph.SetEdge(d.graph.NewEdge(storageVolumeNode, deviceNode))
						if storageVolumeNode.Project != d.id {
							// If the volume is in a different project (inherited from 'default'), we need to connect the device to the project node.
							if !d.graph.HasEdgeBetween(d.localRoot.ID(), deviceNode.ID()) {
								d.graph.SetEdge(d.graph.NewEdge(d.localRoot, deviceNode))
							}
						}
					}

					d.muGraph.Unlock()
					d.muHumanIDtoNodeID.Unlock()
				} else {
					// The disk device is not dependent on a storage volume, so it is dependent on the project node and the storage pool directly.
					rank := d.localRoot.Rank() + 1
					var deviceNode *nodes.DeviceNode
					if isProfile {
						deviceNode = nodes.NewDeviceNode(d.id, fmt.Sprintf("profile-%s", profile.Name), devType, devName, devConfig, rank)
					} else {
						deviceNode = nodes.NewDeviceNode(d.id, fmt.Sprintf("instance-%s", instance.Name), devType, devName, devConfig, rank)
					}

					d.muHumanIDtoNodeID.RLock()
					storagePoolID, ok := d.humanIDtoNodeID[nodes.GenerateStoragePoolHumanID(devConfig["pool"])]
					if !ok {
						d.muHumanIDtoNodeID.RUnlock()
						return nil, fmt.Errorf("The storage pool node %q is not in the graph", devConfig["pool"])
					}

					d.muHumanIDtoNodeID.RUnlock()
					d.muGraph.RLock()
					storagePoolNode, ok := d.graph.Node(storagePoolID).(*nodes.StoragePoolNode)
					if !ok {
						d.muGraph.RUnlock()
						return nil, fmt.Errorf("The node at %d  is not a storage pool", storagePoolID)
					}

					d.muGraph.RUnlock()
					deviceNodes = append(deviceNodes, deviceNode)
					d.muGraph.Lock()
					d.muHumanIDtoNodeID.Lock()
					d.graph.AddNode(deviceNode)
					d.humanIDtoNodeID[deviceNode.HumanID()] = deviceNode.ID()
					d.graph.SetEdge(d.graph.NewEdge(d.localRoot, deviceNode))
					d.graph.SetEdge(d.graph.NewEdge(storagePoolNode, deviceNode))
					d.muGraph.Unlock()
					d.muHumanIDtoNodeID.Unlock()
				}
			}
		} else if devType == "nic" {
			// A nic device can be dependent on a network
			if devConfig["network"] != "" {
				d.muHumanIDtoNodeID.RLock()
				networkNodeID, exists := d.humanIDtoNodeID[nodes.GenerateNetworkHumanID(d.id, devConfig["network"])]
				d.muHumanIDtoNodeID.RUnlock()
				if !exists {
					if isProfile {
						return nil, fmt.Errorf("Profile %q nic device %q depends on a non-existing network %q", profile.Name, devName, devConfig["network"])
					} else {
						return nil, fmt.Errorf("Instance %q nic device %q depends on a non-existing network %q", instance.Name, devName, devConfig["network"])
					}
				}

				d.muGraph.RLock()
				networkNode, ok := d.graph.Node(networkNodeID).(*nodes.NetworkNode)
				if !ok {
					d.muGraph.RUnlock()
					return nil, fmt.Errorf("The network node %q is not in the graph", devConfig["network"])
				}

				d.muGraph.RUnlock()
				rank := networkNode.Rank() + 1
				aclNodes := make([]*nodes.NetworkACLNode, 0)
				if devConfig["security.acls"] != "" {
					acls := strings.Split(devConfig["security.acls"], ",")
					for _, acl := range acls {
						d.muHumanIDtoNodeID.RLock()
						networkACLid, exists := d.humanIDtoNodeID[nodes.GenerateNetworkACLHumanID(d.id, acl)]
						d.muHumanIDtoNodeID.RUnlock()
						if !exists {
							if isProfile {
								return nil, fmt.Errorf("Profile %q nic device %q depends on a non-existing network ACL %q", profile.Name, devName, acl)
							} else {
								return nil, fmt.Errorf("Instance %q nic device %q depends on a non-existing network ACL %q", instance.Name, devName, acl)
							}
						}

						d.muGraph.RLock()
						networkACLNode, ok := d.graph.Node(networkACLid).(*nodes.NetworkACLNode)
						if !ok {
							d.muGraph.RUnlock()
							return nil, fmt.Errorf("The network ACL node %q is not in the graph", acl)
						}

						aclNodes = append(aclNodes, networkACLNode)
						d.muGraph.RUnlock()
						if networkACLNode.Rank() > rank {
							rank = networkACLNode.Rank() + 1
						}
					}
				}

				d.muGraph.Lock()
				d.muHumanIDtoNodeID.Lock()
				var deviceNode *nodes.DeviceNode
				if isProfile {
					deviceNode = nodes.NewDeviceNode(d.id, fmt.Sprintf("profile-%s", profile.Name), devType, devName, devConfig, rank)
				} else {
					deviceNode = nodes.NewDeviceNode(d.id, fmt.Sprintf("instance-%s", instance.Name), devType, devName, devConfig, rank)
				}

				deviceNodes = append(deviceNodes, deviceNode)
				d.graph.AddNode(deviceNode)
				d.humanIDtoNodeID[deviceNode.HumanID()] = deviceNode.ID()
				d.graph.SetEdge(d.graph.NewEdge(networkNode, deviceNode))
				if networkNode.Project != d.id {
					// If the network is in a different project (inherited from 'default'), we need to connect the device to the project node.
					d.graph.SetEdge(d.graph.NewEdge(d.localRoot, deviceNode))
				}

				// Attach the network ACLs to the nic device if any.
				for _, aclNode := range aclNodes {
					d.graph.SetEdge(d.graph.NewEdge(aclNode, deviceNode))
				}

				d.muGraph.Unlock()
				d.muHumanIDtoNodeID.Unlock()
			} else {
				// The nic device is not dependent on a network, so it is dependent on the project node.
				d.muGraph.Lock()
				d.muHumanIDtoNodeID.Lock()
				rank := d.localRoot.Rank() + 1
				var deviceNode *nodes.DeviceNode
				if isProfile {
					deviceNode = nodes.NewDeviceNode(d.id, fmt.Sprintf("profile-%s", profile.Name), devType, devName, devConfig, rank)
				} else {
					deviceNode = nodes.NewDeviceNode(d.id, fmt.Sprintf("instance-%s", instance.Name), devType, devName, devConfig, rank)
				}

				deviceNodes = append(deviceNodes, deviceNode)
				d.graph.AddNode(deviceNode)
				d.humanIDtoNodeID[deviceNode.HumanID()] = deviceNode.ID()
				d.graph.SetEdge(d.graph.NewEdge(d.localRoot, deviceNode))
				d.muGraph.Unlock()
				d.muHumanIDtoNodeID.Unlock()
			}
		} else {
			// Connect the device to the project node.
			d.muGraph.Lock()
			d.muHumanIDtoNodeID.Lock()
			var deviceNode *nodes.DeviceNode
			if isProfile {
				deviceNode = nodes.NewDeviceNode(d.id, fmt.Sprintf("profile-%s", profile.Name), devType, devName, devConfig, d.localRoot.Rank()+1)
			} else {
				deviceNode = nodes.NewDeviceNode(d.id, fmt.Sprintf("instance-%s", instance.Name), devType, devName, devConfig, d.localRoot.Rank()+1)
			}

			deviceNodes = append(deviceNodes, deviceNode)
			d.graph.AddNode(deviceNode)
			d.humanIDtoNodeID[deviceNode.HumanID()] = deviceNode.ID()
			d.graph.SetEdge(d.graph.NewEdge(d.localRoot, deviceNode))
			d.muGraph.Unlock()
			d.muHumanIDtoNodeID.Unlock()
		}

		d.logger.Info("Device added.", logrus.Fields{"id": d.id, "device": devName})
	}

	return deviceNodes, nil
}

func (d *DAGWorker) addProfiles(profiles []api.Profile) error {
	for _, profile := range profiles {
		profileDevices := profile.Devices
		deviceNodes, err := d.addDevices(profileDevices, profile)
		if err != nil {
			return err
		}

		// Attach the devices to the profile node
		maxRank := uint(0)
		for _, deviceNode := range deviceNodes {
			if deviceNode.Rank() > maxRank {
				maxRank = deviceNode.Rank()
			}
		}

		d.muGraph.Lock()
		d.muHumanIDtoNodeID.Lock()
		profileNode := nodes.NewProfileNode(d.id, profile.Name, profile, maxRank+1)
		d.graph.AddNode(profileNode)
		d.humanIDtoNodeID[profileNode.HumanID()] = profileNode.ID()
		for _, deviceNode := range deviceNodes {
			d.graph.SetEdge(d.graph.NewEdge(deviceNode, profileNode))
		}

		d.logger.Info("Profile added.", logrus.Fields{"id": d.id, "profile": profile.Name})
		d.muGraph.Unlock()
		d.muHumanIDtoNodeID.Unlock()
	}

	return nil
}

func (d *DAGWorker) addInstances(instances []api.Instance) error {
	for _, inst := range instances {
		profiles := inst.Profiles
		localDevices := inst.Devices
		maxRank := uint(0)

		// profiles to instance connections.
		profileNodes := make([]*nodes.ProfileNode, 0)
		for _, profile := range profiles {
			d.muHumanIDtoNodeID.RLock()
			profileID, exists := d.humanIDtoNodeID[nodes.GenerateProfileHumanID(d.id, profile)]
			d.muHumanIDtoNodeID.RUnlock()
			if !exists {
				return fmt.Errorf("Instance %q depends on a non-existing profile %q", inst.Name, profile)
			}

			d.muGraph.RLock()
			profileNode, ok := d.graph.Node(profileID).(*nodes.ProfileNode)
			if !ok {
				d.muGraph.RUnlock()
				return fmt.Errorf("The profile node %q is not in the graph", profile)
			}

			if profileNode.Rank() > maxRank {
				maxRank = profileNode.Rank()
			}

			profileNodes = append(profileNodes, profileNode)
			d.muGraph.RUnlock()
		}

		// Create device nodes that are not part of any profile.
		localDeviceNodes, err := d.addDevices(localDevices, inst)
		if err != nil {
			return err
		}

		for _, deviceNode := range localDeviceNodes {
			if deviceNode.Rank() > maxRank {
				maxRank = deviceNode.Rank()
			}
		}

		// Add the instance node and connect its dependencies (profiles and local devices if any).
		d.muGraph.Lock()
		d.muHumanIDtoNodeID.Lock()
		instanceNode := nodes.NewInstanceNode(d.id, inst.Name, inst, maxRank+1)
		d.graph.AddNode(instanceNode)
		d.humanIDtoNodeID[instanceNode.HumanID()] = instanceNode.ID()
		for _, profileNode := range profileNodes {
			d.graph.SetEdge(d.graph.NewEdge(profileNode, instanceNode))
		}

		for _, deviceNode := range localDeviceNodes {
			d.graph.SetEdge(d.graph.NewEdge(deviceNode, instanceNode))
		}

		d.muGraph.Unlock()
		d.muHumanIDtoNodeID.Unlock()
	}

	return nil
}

func (d *DAGWorker) exportInstances() error {
	waitDefaultProfiles := make(chan struct{})
	go func() {
		for msg := range d.incoming {
			switch msg.Content {
			case "DEFAULT_PROFILES":
				close(waitDefaultProfiles)
			}
		}

		close(waitDefaultProfiles)
	}()

	if d.id != "default" {
		<-waitDefaultProfiles
	}

	// 1) First, process the 'profiles'
	profiles, err := d.client.GetProfiles()
	if err != nil {
		return err
	}

	if d.id != "default" {
		if shared.IsTrue(d.rootFeatures["features.profiles"]) {
			// We don't inherit from 'default' project profiles. No need to filter.
			err = d.addProfiles(profiles)
			if err != nil {
				return err
			}
		} else {
			// We inherit from 'default' project profiles, we might need to filter the profiles.
			filteredProfiles := make([]api.Profile, 0)
			d.muHumanIDtoNodeID.Lock()
			for _, profile := range profiles {
				_, exists := d.humanIDtoNodeID[nodes.GenerateProfileHumanID("default", profile.Name)]
				if !exists {
					filteredProfiles = append(filteredProfiles, profile)
				}
			}

			d.muHumanIDtoNodeID.Unlock()
			err = d.addProfiles(filteredProfiles)
			if err != nil {
				return err
			}
		}
	} else {
		err = d.addProfiles(profiles)
		if err != nil {
			return err
		}

		if d.broadcaster != nil {
			d.broadcaster.Broadcast(Message{SenderID: d.id, Content: "DEFAULT_PROFILES"})
		}
	}

	// 2) Then process the 'instances'
	containers, err := d.client.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return err
	}

	vms, err := d.client.GetInstances(api.InstanceTypeVM)
	if err != nil {
		return err
	}

	err = d.addInstances(append(containers, vms...))
	if err != nil {
		return err
	}

	return nil
}

func (d *DAGWorker) Start() error {
	localWG := sync.WaitGroup{}
	localErrorChan := make(chan error, 2)
	globalNetworking := make(map[string]bool)
	waitGlobalNetworking := make(chan struct{})
	globalStorage := make(map[string]bool)
	waitGlobalStorage := make(chan struct{})
	go func() {
		for msg := range d.incoming {
			switch msg.Content {
			case "GLOBAL_NETWORKING":
				d.logger.Debug("GLOBAL_NETWORKING received !", logrus.Fields{"id": d.id})
				globalNetworking[msg.SenderID] = true
				if len(globalNetworking) == int(d.totalWorkers) {
					d.logger.Debug("closing GLOBAL_NETWORKING !", logrus.Fields{"id": d.id})
					close(waitGlobalNetworking)
				}
			case "GLOBAL_STORAGE":
				d.logger.Debug("GLOBAL_STORAGE received !", logrus.Fields{"id": d.id})
				globalStorage[msg.SenderID] = true
				if len(globalStorage) == int(d.totalWorkers) {
					d.logger.Debug("closing GLOBAL_STORAGE !", logrus.Fields{"id": d.id})
					close(waitGlobalStorage)
				}
			}
		}

		close(waitGlobalNetworking)
		close(waitGlobalStorage)
	}()

	localWG.Add(2)
	// Project-level networking thread
	go func() {
		d.logger.Info("Starting to export networking...", logrus.Fields{"id": d.id})
		defer localWG.Done()
		err := d.exportNetworking()
		if err != nil {
			d.logger.Error("Failing to export networking.", logrus.Fields{"id": d.id, "error": err})
			localErrorChan <- err
			return
		}

		d.logger.Info("Networking has been exported successfully!", logrus.Fields{"id": d.id})
	}()

	// Project-level storage thread
	go func() {
		d.logger.Info("Starting to export storage...", logrus.Fields{"id": d.id})
		defer localWG.Done()
		err := d.exportStorage()
		if err != nil {
			d.logger.Error("Failing to export storage.", logrus.Fields{"id": d.id, "error": err})
			localErrorChan <- err
			return
		}

		d.logger.Info("Storage has been exported successfully!", logrus.Fields{"id": d.id})
	}()

	go func() {
		d.logger.Info("Waiting for local networking and storage to be processed...", logrus.Fields{"id": d.id})
		localWG.Wait()
		close(localErrorChan)
	}()

	// Collect and return errors
	var errorsGroup []error
	for err := range localErrorChan {
		errorsGroup = append(errorsGroup, err)
	}

	if len(errorsGroup) > 0 {
		formattedErrors := ""
		for _, err := range errorsGroup {
			formattedErrors += fmt.Sprintf("- %v\n", err)
		}

		return fmt.Errorf("Error(s) building the DAG for ID %q:\n%s", d.id, formattedErrors)
	}

	if d.broadcaster != nil {
		d.logger.Info("Wait for all networking and storage to be processed in all projects...", logrus.Fields{"id": d.id})
		<-waitGlobalNetworking
		<-waitGlobalStorage
	}

	d.logger.Info("All networking and storage have been processed in all projects!", logrus.Fields{"id": d.id})

	localWG.Add(1)
	localErrorChan = make(chan error, 1)
	go func() {
		d.logger.Info("Starting to export instances...", logrus.Fields{"id": d.id})
		defer localWG.Done()
		err := d.exportInstances()
		if err != nil {
			localErrorChan <- err
		}
	}()

	go func() {
		localWG.Wait()
		close(localErrorChan)
	}()

	for err := range localErrorChan {
		errorsGroup = append(errorsGroup, err)
	}

	if len(errorsGroup) > 0 {
		return fmt.Errorf("Error(s) building the DAG for ID %q: %v\n", d.id, errorsGroup)
	}

	// Unregister the worker from the broadcaster
	if d.broadcaster != nil {
		d.logger.Info("Unregister worker broadcaster", logrus.Fields{"id": d.id})
		d.broadcaster.Unregister(d.id)
	}

	d.logger.Info("DAG worker has finished processing successfully!", logrus.Fields{"id": d.id})
	return nil
}
