package core

import (
	"errors"
	"fmt"
	"strings"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
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
	projectNodeHID string,
	graph *DAG,
	nzs []api.NetworkZone,
	nodeID *int64,
	l *Logger,
) error {
	for _, nz := range nzs {
		nzNodeHID := generateNetworkZoneHumanID(nz.Name)
		graph.AddNode(*nodeID, nzNodeHID, nz)
		graph.SetEdge(projectNodeHID, nzNodeHID)
		*nodeID++

		l.Info("Adding network zone %q", map[string]any{"hid": nzNodeHID, "name": nz.Name})

		// Add network zone records (only dependent on their network zone)
		records, err := client.GetNetworkZoneRecords(nz.Name)
		if err != nil {
			l.Error("Failed to get network zone records for network zone %q: %v", map[string]any{"nzName": nz.Name, "err": err})
			return err
		}

		for _, record := range records {
			recordNodeHID := generateNetworkZoneRecordHumanID(nz.Name, record.Name)
			graph.AddNode(*nodeID, recordNodeHID, record)
			graph.SetEdge(nzNodeHID, recordNodeHID)
			// A net zone record is dependent on a Project node.
			graph.SetEdge(projectNodeHID, recordNodeHID)
			*nodeID++

			l.Info("Adding network zone record %q", map[string]any{"hid": recordNodeHID, "name": record.Name})
		}
	}

	return nil
}

func addNetworkLBs(
	client lxd.InstanceServer,
	projectName string,
	graph *DAG,
	netName string,
	netNodeHID string,
	nodeID *int64,
	l *Logger,
) error {
	lbs, err := client.GetNetworkLoadBalancers(netName)
	if err != nil {
		l.Error("Failed to get network load balancers for network", map[string]any{"netName": netName, "err": err})
		return err
	}

	for _, lb := range lbs {
		lbNodeHID := generateNetworkLBHumanID(projectName, netName, string(*nodeID))
		if graph.HasNode(lbNodeHID) {
			l.Debug("Load balancer %q already in the graph", map[string]any{"hid": lbNodeHID})
			continue
		}

		graph.AddNode(*nodeID, lbNodeHID, lb)
		*nodeID++
		l.Info("Adding network load balancer", map[string]any{"hid": lbNodeHID, "netName": netName})
		graph.SetEdge(netNodeHID, lbNodeHID)
	}

	return nil
}

func addNetworkForwards(
	client lxd.InstanceServer,
	projectName string,
	graph *DAG,
	netName string,
	netNodeHID string,
	nodeID *int64,
	l *Logger,
) error {
	forwards, err := client.GetNetworkForwards(netName)
	if err != nil {
		l.Error("Failed to get network forwards for network", map[string]any{"netName": netName, "err": err})
		return err
	}

	for _, forward := range forwards {
		forwardNodeHID := generateNetworkForwardHumanID(projectName, netName, string(*nodeID))
		if graph.HasNode(forwardNodeHID) {
			l.Debug("Forward already in the graph", map[string]any{"hid": forwardNodeHID})
			continue
		}

		graph.AddNode(*nodeID, forwardNodeHID, forward)
		*nodeID++
		graph.SetEdge(netNodeHID, forwardNodeHID)

		l.Info("Adding network forward", map[string]any{"hid": forwardNodeHID, "netName": netName})
	}

	return nil
}

func addNetworks(
	client lxd.InstanceServer,
	projectName string,
	projectNodeHID string,
	graph *DAG,
	networks []api.Network,
	inheritedNetworks []api.Network,
	nodeID *int64,
	l *Logger,
) error {
	if projectName == "default" {
		for _, net := range networks {
			// We need to track non-managed networks:
			// If a managed external network (having a non-managed network interface as a parent) in the source cluster needs to be created in the destination cluster,
			// we need to know if the destination cluster has the same non-managed network interface in order to be created.
			if !net.Managed {
				netNodeHID := generateNetworkHumanID(projectName, net.Name)
				graph.AddNode(*nodeID, netNodeHID, net)
				*nodeID++
				l.Info("Adding non-managed network", map[string]any{"hid": netNodeHID, "name": net.Name})
			}
		}
	}

	checkForNetZone := func(net api.Network) (nzNodeHID string, err error) {
		for _, nz := range []string{"dns.zone.forward", "dns.zone.reverse.ipv4", "dns.zone.reverse.ipv6"} {
			if net.Config[nz] != "" {
				nzNodeHID = generateNetworkZoneHumanID(net.Config[nz])
				if !graph.HasNode(nzNodeHID) {
					return "", fmt.Errorf("Network %q depends on a non-existing network zone %q", net.Name, net.Config["dns.zone.forward"])
				}

				l.Info("Network has network zone", map[string]any{"netName": net.Name, "nzName": net.Config[nz]})
				break
			}
		}

		return nzNodeHID, nil
	}

	for _, net := range networks {
		if net.Managed {
			var netNodeHID string
			if net.Type == "bridge" {
				netNodeHID = generateNetworkHumanID(projectName, net.Name)
				graph.AddNode(*nodeID, netNodeHID, net)
				*nodeID++

				l.Info("Adding managed bridge network", map[string]any{"hid": netNodeHID, "name": net.Name})

				// Connect to the project node
				graph.SetEdge(projectNodeHID, netNodeHID)

				// Add the potential network forwards
				err := addNetworkForwards(client, projectName, graph, net.Name, netNodeHID, nodeID, l)
				if err != nil {
					return err
				}
			}

			if shared.ValueInSlice(net.Type, []string{"macvlan", "physical", "sriov"}) {
				parentIface := net.Config["parent"]
				netNodeHID = generateNetworkHumanID(projectName, net.Name)
				graph.AddNode(*nodeID, netNodeHID, net)
				*nodeID++
				l.Info("Adding managed physical network", map[string]any{"hid": netNodeHID, "name": net.Name})

				if parentIface != "" {
					// Connect to the parent non-managed network
					if !graph.HasNode(generateNetworkHumanID("default", parentIface)) {
						return fmt.Errorf("The parent network %q is not in the graph", parentIface)
					}

					graph.SetEdge(generateNetworkHumanID("default", parentIface), netNodeHID)
				}
			}

			// Check if the network is dependent on a network zone
			if netNodeHID != "" {
				nzHID, err := checkForNetZone(net)
				if err != nil {
					return err
				}

				if nzHID != "" {
					graph.SetEdge(nzHID, netNodeHID)
					l.Info("Adding network zone dependency", map[string]any{"netName": net.Name, "nzHID": nzHID})
				}
			}
		}
	}

	// Now process potential 'ovn' networks
	for _, net := range networks {
		if net.Managed && net.Type == "ovn" {
			netNodeHID := generateNetworkHumanID(projectName, net.Name)
			graph.AddNode(*nodeID, netNodeHID, net)
			*nodeID++

			l.Info("Adding managed ovn network", map[string]any{"hid": netNodeHID, "name": net.Name})

			// Connect to the project node
			graph.SetEdge(projectNodeHID, netNodeHID)
			l.Info("Adding project dependency", map[string]any{"netName": net.Name, "projectHID": projectNodeHID})

			// Add the potential network load balancers
			err := addNetworkLBs(client, projectName, graph, net.Name, netNodeHID, nodeID, l)
			if err != nil {
				return err
			}

			// Add the potential network forwards
			err = addNetworkForwards(client, projectName, graph, net.Name, netNodeHID, nodeID, l)
			if err != nil {
				return err
			}

			// Add the parent network (either a managed bridge or a managed physical network)
			parentNetwork := net.Config["network"]
			if parentNetwork != "" {
				var parentNetHID string
				if inheritedNetworks == nil {
					parentNetHID := generateNetworkHumanID(projectName, parentNetwork)
					if !graph.HasNode(parentNetHID) {
						return fmt.Errorf("The parent network %q is not in the graph", parentNetwork)
					}
				} else {
					for _, net := range inheritedNetworks {
						if net.Name == parentNetwork {
							parentNetHID = generateNetworkHumanID("default", parentNetwork)
							if !graph.HasNode(parentNetHID) {
								return fmt.Errorf("The parent network %q is not in the graph", parentNetwork)
							}
						}
					}

					if parentNetHID == "" {
						parentNetHID = generateNetworkHumanID(projectName, parentNetwork)
						if !graph.HasNode(parentNetHID) {
							return fmt.Errorf("The parent network %q is not in the graph", parentNetwork)
						}
					}
				}

				graph.SetEdge(parentNetHID, netNodeHID)
				l.Info("Adding parent network dependency", map[string]any{"netName": net.Name, "parentNetHID": parentNetHID})
			}

			// Check if the network is dependent on a network zone
			if netNodeHID != "" {
				nzHID, err := checkForNetZone(net)
				if err != nil {
					return err
				}

				if nzHID != "" {
					graph.SetEdge(nzHID, netNodeHID)
					l.Info("Adding network zone dependency for OVN", map[string]any{"netName": net.Name, "nzHID": nzHID})
				}
			}
		}
	}

	return nil
}

func addNetworkPeers(
	client lxd.InstanceServer,
	projectName string,
	graph *DAG,
	nodeID *int64,
	l *Logger,
) error {
	netNodes := make(map[string]*Node)
	for hid, node := range graph.Nodes {
		if strings.HasPrefix(hid, networkPrefix) {
			netNodes[hid] = node
		}
	}

	networksPrefixPerProject := fmt.Sprintf("%s%s_", networkPrefix, projectName)
	for netNodeHID, node := range netNodes {
		if !strings.HasPrefix(netNodeHID, networksPrefixPerProject) {
			l.Debug("Skipping network %q while looking for network peers", map[string]any{"hid": netNodeHID})
			continue
		}

		// Get the source network node
		net, ok := node.Data.(api.Network)
		if !ok {
			return fmt.Errorf("The network node %q is not in the graph", netNodeHID)
		}

		// Only managed 'ovn' networks support peering.
		if net.Managed && net.Type == "ovn" {
			// Check if it has peers
			peers, err := client.GetNetworkPeers(net.Name)
			if err != nil {
				l.Error("Failed to get network peers for network", map[string]any{"netName": net.Name, "err": err})
				return err
			}

			for _, peer := range peers {
				// Check target network and project
				var targetNetworkNodeHID string
				targetNetworkHumanID := generateNetworkHumanID(peer.TargetProject, peer.TargetNetwork)
				for targetHumanID, _ := range netNodes {
					if targetHumanID == targetNetworkHumanID {
						targetNetworkNodeHID = targetNetworkHumanID
						break
					}
				}

				if targetNetworkNodeHID == "" {
					return fmt.Errorf("No target network could be found for the peer %s", peer.Name)
				}

				netPeerNodeHID := generateNetworkPeerHumanID(projectName, net.Name, peer.Name)
				graph.AddNode(*nodeID, netPeerNodeHID, peer)
				*nodeID++

				l.Info("Adding network peer", map[string]any{"hid": netPeerNodeHID, "name": peer.Name})

				graph.SetEdge(netNodeHID, netPeerNodeHID)
				graph.SetEdge(targetNetworkNodeHID, netPeerNodeHID)
			}
		}
	}

	return nil
}

func addNetworkACLs(
	client lxd.InstanceServer,
	projectName string,
	projectNodeHID string,
	graph *DAG,
	nodeID *int64,
	l *Logger,
) error {
	acls, err := client.GetNetworkACLs()
	if err != nil {
		l.Error("Failed to get network ACLs", map[string]any{"err": err})
		return err
	}

	if len(acls) == 0 {
		return nil
	}

	// Get a local copy of the peer node references,
	// as an ACL can be dependant on a network peer if an ACL rule
	// uses a network subject selector
	projectNetworkPeerPrefix := fmt.Sprintf("%s%s_", networkPeerPrefix, projectName)
	netPeers := make(map[string]*Node)
	for hid, node := range graph.Nodes {
		if strings.HasPrefix(hid, projectNetworkPeerPrefix) {
			netPeers[hid] = node
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

	peerNodeHIDs := make([]string, 0)
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
					l.Error("Failed to extract network and peer from ACL ingress source rule", map[string]any{"err": err})
					return err
				}

				// Attempt to find the right network peer node reference
				peerNodeHID := generateNetworkPeerHumanID(projectName, networkName, peerName)
				if !graph.HasNode(peerNodeHID) {
					return fmt.Errorf("Could not find the network peer (%s/%s) node reference for ACL ingress source rule %q", networkName, peerName, acl.Name)
				}

				peerNodeHIDs = append(peerNodeHIDs, peerNodeHID)
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
					l.Error("Failed to extract network and peer from ACL egress destination rule", map[string]any{"err": err})
					return err
				}

				peerNodeHID := generateNetworkPeerHumanID(projectName, networkName, peerName)
				if !graph.HasNode(peerNodeHID) {
					return fmt.Errorf("Could not find the network peer (%s/%s) node reference for ACL egress destination rule %q", networkName, peerName, acl.Name)
				}

				peerNodeHIDs = append(peerNodeHIDs, peerNodeHID)
			}
		}

		aclNodeHID := generateNetworkACLHumanID(projectName, acl.Name)
		graph.AddNode(*nodeID, aclNodeHID, acl)
		*nodeID++
		l.Info("Adding network ACL", map[string]any{"hid": aclNodeHID, "name": acl.Name})
		graph.SetEdge(projectNodeHID, aclNodeHID)
		for _, peerNodeHID := range peerNodeHIDs {
			graph.SetEdge(peerNodeHID, aclNodeHID)
			l.Info("Adding network peer dependency to network ACL", map[string]any{"aclName": acl.Name, "peerHID": peerNodeHID})
		}
	}

	return nil
}

func buildNetworkingInventory(
	client lxd.InstanceServer,
	graph *DAG,
	defaultProjectHID string,
	otherProjectHIDs map[string]string,
	featuresPerProject map[string]map[string]string,
	nodeID *int64,
	l *Logger,
) error {
	// 1) Export network zones
	nzs, err := client.GetNetworkZones()
	if err != nil {
		return err
	}

	err = addNetworkZones(client, defaultProjectHID, graph, nzs, nodeID, l)
	if err != nil {
		return err
	}

	for projectName, projectHID := range otherProjectHIDs {
		nzs, err := client.UseProject(projectName).GetNetworkZones()
		if err != nil {
			return err
		}

		if shared.IsTrue(featuresPerProject[projectName]["features.networks.zones"]) {
			err = addNetworkZones(client.UseProject(projectName), projectHID, graph, nzs, nodeID, l)
			if err != nil {
				return err
			}
		} else {
			// it means I inherit from 'default' project network zones
			// and that some of the network zones we get there are already in the graph.
			// We need to have a filter to only add the network zones that are not already in the graph.
			filteredNetworkZones := make([]api.NetworkZone, 0)
			for _, nz := range nzs {
				if !graph.HasNode(generateNetworkZoneHumanID(nz.Name)) {
					filteredNetworkZones = append(filteredNetworkZones, nz)
				}
			}

			err = addNetworkZones(client.UseProject(projectName), projectHID, graph, filteredNetworkZones, nodeID, l)
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

	err = addNetworks(client, "default", defaultProjectHID, graph, nets, nil, nodeID, l)
	if err != nil {
		return err
	}

	for projectName, projectHID := range otherProjectHIDs {
		nets, err := client.UseProject(projectName).GetNetworks()
		if err != nil {
			return err
		}

		if shared.IsTrue(featuresPerProject[projectName]["features.networks"]) {
			// We don't inherit from 'default' project networks.
			err = addNetworks(client.UseProject(projectName), projectName, projectHID, graph, nets, nil, nodeID, l)
			if err != nil {
				return err
			}
		} else {
			// We inherit from 'default' project networks.
			inheritedNetworks := make([]api.Network, 0)
			filteredNetworks := make([]api.Network, 0)
			for _, net := range nets {
				if graph.HasNode(generateNetworkHumanID("default", net.Name)) {
					inheritedNetworks = append(inheritedNetworks, net)
				} else {
					filteredNetworks = append(filteredNetworks, net)
				}
			}

			err = addNetworks(client.UseProject(projectName), projectName, projectHID, graph, filteredNetworks, inheritedNetworks, nodeID, l)
			if err != nil {
				return err
			}
		}
	}

	// 3) Export network peers
	err = addNetworkPeers(client, "default", graph, nodeID, l)
	if err != nil {
		return err
	}

	for projectName := range otherProjectHIDs {
		err = addNetworkPeers(client.UseProject(projectName), projectName, graph, nodeID, l)
		if err != nil {
			return err
		}
	}

	// 4) Export network ACLs
	err = addNetworkACLs(client, "default", defaultProjectHID, graph, nodeID, l)
	if err != nil {
		return err
	}

	for projectName, projectHID := range otherProjectHIDs {
		err = addNetworkACLs(client.UseProject(projectName), projectName, projectHID, graph, nodeID, l)
		if err != nil {
			return err
		}
	}

	return nil
}

func addStorageVolumes(
	client lxd.InstanceServer,
	projectName string,
	graph *DAG,
	nodeID *int64,
	pools map[string]string,
	inherit bool,
) error {
	for poolName, poolHID := range pools {
		poolNode, ok := graph.Nodes[poolHID].Data.(*api.StoragePool)
		if !ok {
			return fmt.Errorf("The pool node %q is not in the graph", poolHID)
		}

		poolDriver := poolNode.Driver
		volumes, err := client.GetStoragePoolVolumes(poolName)
		if err != nil {
			return err
		}

		for _, volume := range volumes {
			var volumeHID string
			if inherit {
				// Check if the volume is already in the graph
				volumeHID = generateStorageVolumeHumanID(projectName, poolName, volume.Name, volume.Location)
				if graph.HasNode(volumeHID) {
					continue
				}
			}

			if volumeHID == "" {
				volumeHID = generateStorageVolumeHumanID(projectName, poolName, volume.Name, volume.Location)
			}

			graph.AddNode(*nodeID, volumeHID, volume)
			*nodeID++
			graph.SetEdge(poolHID, volumeHID)

			// Add project dependency
			if projectName == "default" {
				graph.SetEdge(generateProjectHumanID("default"), volumeHID)
			} else {
				projectHID := generateProjectHumanID(projectName)
				if !inherit {
					graph.SetEdge(projectHID, volumeHID)
				} else {
					// If the volume is already in the graph,
					// the project dependency is already set.
					if !graph.HasNode(volumeHID) {
						graph.SetEdge(projectHID, volumeHID)
					}
				}
			}

			// Add the storage volume snapshots
			snapshots, err := client.GetStoragePoolVolumeSnapshots(poolName, volume.Type, volume.Name)
			if err != nil {
				return err
			}

			for _, snapshot := range snapshots {
				snapshotHID := generateStorageVolumeSnapshotHumanID(projectName, poolDriver, poolName, volume.Name, snapshot.Name)
				graph.AddNode(*nodeID, snapshotHID, snapshot)
				*nodeID++
				graph.SetEdge(volumeHID, snapshotHID)
			}
		}
	}

	return nil
}

func addStorageBucketsAndKeys(
	client lxd.InstanceServer,
	projectName string,
	graph *DAG,
	nodeID *int64,
	pools map[string]string,
	inherit bool,
) error {
	for poolName, poolHID := range pools {
		poolNode, ok := graph.Nodes[poolHID].Data.(*api.StoragePool)
		if !ok {
			return fmt.Errorf("The pool node %q is not in the graph", poolHID)
		}

		poolDriver := poolNode.Driver
		if !shared.ValueInSlice(poolDriver, []string{"btrfs", "cephobject", "dir", "lvm", "zfs"}) {
			continue
		}

		buckets, err := client.GetStoragePoolBuckets(poolName)
		if err != nil {
			return err
		}

		for _, bucket := range buckets {
			var bucketHID string
			if inherit {
				// Check if the bucket is already in the graph
				bucketHID = generateStorageBucketHumanID(projectName, poolDriver, poolName, bucket.Name)
				if graph.HasNode(bucketHID) {
					continue
				}
			}

			if bucketHID == "" {
				bucketHID = generateStorageBucketHumanID(projectName, poolDriver, poolName, bucket.Name)
			}

			graph.AddNode(*nodeID, bucketHID, bucket)
			*nodeID++
			graph.SetEdge(poolHID, bucketHID)

			// Add project dependency
			if projectName == "default" {
				graph.SetEdge(generateProjectHumanID("default"), bucketHID)
			} else {
				projectHID := generateProjectHumanID(projectName)
				if !inherit {
					graph.SetEdge(projectHID, bucketHID)
				} else {
					// If the bucket is already in the graph,
					// the project dependency is already set.
					if !graph.HasNode(bucketHID) {
						graph.SetEdge(projectHID, bucketHID)
					}
				}
			}

			// Add the bucket keys
			keys, err := client.GetStoragePoolBucketKeys(poolName, bucket.Name)
			if err != nil {
				return err
			}

			for _, key := range keys {
				bucketKeyHID := generateStorageBucketKeyHumanID(projectName, poolDriver, poolName, bucket.Name, key.Name)
				graph.AddNode(*nodeID, bucketKeyHID, key)
				*nodeID++
				graph.SetEdge(bucketHID, bucketKeyHID)
			}
		}
	}

	return nil
}

func buildStorageInventory(
	client lxd.InstanceServer,
	graph *DAG,
	otherProjectHIDs map[string]string,
	featuresPerProject map[string]map[string]string,
	nodeID *int64,
) error {
	// 1) First, process the 'storage pools'
	storagePoolNames, err := client.GetStoragePoolNames()
	if err != nil {
		return err
	}

	poolHIDs := make(map[string]string, 0)
	for _, poolName := range storagePoolNames {
		pool, _, err := client.GetStoragePool(poolName)
		if err != nil {
			return err
		}

		poolHID := generateStoragePoolHumanID(pool.Name) // A storage pool is always dependent on the root node.
		graph.AddNode(*nodeID, poolHID, pool)
		*nodeID++
		poolHIDs[pool.Name] = poolHID
	}

	// 2) Then, process the 'storage volumes'
	err = addStorageVolumes(client, "default", graph, nodeID, poolHIDs, false)
	if err != nil {
		return err
	}

	for projectName := range otherProjectHIDs {
		if shared.IsTrue(featuresPerProject[projectName]["features.storage.volumes"]) {
			err = addStorageVolumes(client.UseProject(projectName), projectName, graph, nodeID, poolHIDs, false)
			if err != nil {
				return err
			}
		} else {
			err = addStorageVolumes(client.UseProject(projectName), projectName, graph, nodeID, poolHIDs, true)
			if err != nil {
				return err
			}
		}
	}

	// 3) Export storage buckets
	err = addStorageBucketsAndKeys(client, "default", graph, nodeID, poolHIDs, false)
	if err != nil {
		return err
	}

	for projectName := range otherProjectHIDs {
		if shared.IsTrue(featuresPerProject[projectName]["features.storage.buckets"]) {
			err = addStorageBucketsAndKeys(client.UseProject(projectName), projectName, graph, nodeID, poolHIDs, false)
			if err != nil {
				return err
			}
		} else {
			err = addStorageBucketsAndKeys(client.UseProject(projectName), projectName, graph, nodeID, poolHIDs, true)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func checkDevicesDependencies(
	projectName string,
	projectHID string,
	graph *DAG,
	devices map[string]map[string]string,
	child any,
	childHID string,
) error {
	var profile api.Profile
	isProfile := false
	var instance api.Instance
	switch child := child.(type) {
	case api.Profile:
		profile = child
		isProfile = true
	case api.Instance:
		instance = child
	default:
		return fmt.Errorf("Invalid child type %T", child)
	}

	for devName, devConfig := range devices {
		devType := devConfig["type"]
		if devType == "disk" {
			source := devConfig["source"]
			if devConfig["pool"] != "" {
				// There is a pool but we don't know if the source is a storage volume or just a path. Let's check.
				storageVolumeHIDs := make([]string, 0)
				for hid, _ := range graph.Nodes {
					if strings.HasPrefix(hid, fmt.Sprintf("%s%s_%s_%s_", storageVolumePrefix, projectName, devConfig["pool"], source)) {
						storageVolumeHIDs = append(storageVolumeHIDs, hid)
					}
				}

				if len(storageVolumeHIDs) > 0 {
					// The disk device is dependent on a storage volume (that can be on multiple locations).
					for _, volumeHID := range storageVolumeHIDs {
						if !graph.HasEdge(projectHID, volumeHID) {
							graph.SetEdge(volumeHID, childHID)
						}

						parents, err := graph.GetParents(volumeHID)
						if err != nil {
							return err
						}

						for _, parent := range parents {
							if strings.HasPrefix(parent.HID, projectPrefix) {
								parentProject := strings.TrimPrefix(parent.HID, projectPrefix)
								if parentProject != projectName {
									// If the volume is in a different project (inherited from 'default'), we need to connect the device to the project node.
									if !graph.HasEdge(projectHID, childHID) {
										graph.SetEdge(projectHID, childHID)
									}
								}
							}
						}
					}
				} else {
					// The disk device is not dependent on a storage volume, so it is dependent on the project node and the storage pool directly.
					storagePoolHID := generateStoragePoolHumanID(devConfig["pool"])
					if !graph.HasEdge(projectHID, childHID) {
						graph.SetEdge(projectHID, childHID)
					}

					if !graph.HasEdge(storagePoolHID, childHID) {
						graph.SetEdge(storagePoolHID, childHID)
					}
				}
			}
		} else if devType == "nic" {
			// A nic device can be dependent on a network
			if devConfig["network"] != "" {
				networkHID := generateNetworkHumanID(projectName, devConfig["network"])
				if !graph.HasNode(networkHID) {
					if isProfile {
						return fmt.Errorf("Profile %q nic device %q depends on a non-existing network %q", profile.Name, devName, devConfig["network"])
					} else {
						return fmt.Errorf("Instance %q nic device %q depends on a non-existing network %q", instance.Name, devName, devConfig["network"])
					}
				}

				aclHIDs := make([]string, 0)
				if devConfig["security.acls"] != "" {
					acls := strings.Split(devConfig["security.acls"], ",")
					for _, acl := range acls {
						networkACLHID := generateNetworkACLHumanID(projectName, acl)
						if !graph.HasNode(networkACLHID) {
							if isProfile {
								return fmt.Errorf("Profile %q nic device %q depends on a non-existing network ACL %q", profile.Name, devName, acl)
							} else {
								return fmt.Errorf("Instance %q nic device %q depends on a non-existing network ACL %q", instance.Name, devName, acl)
							}
						}

						aclHIDs = append(aclHIDs, networkACLHID)
					}
				}

				if !graph.HasEdge(networkHID, childHID) {
					graph.SetEdge(networkHID, childHID)
				}

				// We need to get the ancestors of the network to know to which project it belongs.
				// the GetParents function is not enough because an OVN network can be a child of an other managed network like a bridge or an external net.
				parents, err := graph.GetAncestors(networkHID)
				if err != nil {
					return err
				}

				for _, parent := range parents {
					if strings.HasPrefix(parent.HID, projectPrefix) {
						parentProject := strings.TrimPrefix(parent.HID, projectPrefix)
						if parentProject != projectName {
							// If the network is in a different project (inherited from 'default'),
							// we need to connect the device to the project node.
							if !graph.HasEdge(projectHID, childHID) {
								graph.SetEdge(projectHID, childHID)
							}
						}
					}
				}

				// Attach the network ACLs to the nic device if any.
				for _, aclHID := range aclHIDs {
					graph.SetEdge(aclHID, childHID)
				}
			} else {
				// The nic device is not dependent on a network, so it is dependent on the project node.
				if !graph.HasEdge(projectHID, childHID) {
					graph.SetEdge(projectHID, childHID)
				}
			}
		} else {
			// Connect the child to the project node.
			if !graph.HasEdge(projectHID, childHID) {
				graph.SetEdge(projectHID, childHID)
			}
		}
	}

	return nil
}

func addProfiles(
	projectName string,
	projectHID string,
	graph *DAG,
	profiles []api.Profile,
	nodeID *int64,
) error {
	for _, profile := range profiles {
		profileHID := generateProfileHumanID(projectName, profile.Name)
		graph.AddNode(*nodeID, profileHID, profile)
		*nodeID++

		// Check if a profile depends on other entities through its devices
		err := checkDevicesDependencies(projectName, projectHID, graph, profile.Devices, profile, profileHID)
		if err != nil {
			return err
		}
	}

	return nil
}

func addInstances(
	projectName string,
	projectHID string,
	graph *DAG,
	instances []api.Instance,
	inheritDefaultProfiles bool,
	nodeID *int64,
) error {
	for _, inst := range instances {
		instHID := generateInstanceHumanID(projectName, inst.Name)
		graph.AddNode(*nodeID, instHID, inst)
		*nodeID++

		profiles := inst.Profiles
		localDevices := inst.Devices

		// profiles to instance connections.
		profileHIDs := make([]string, 0)
		for _, profile := range profiles {
			profileHID := generateProfileHumanID(projectName, profile)
			if !graph.HasNode(profileHID) {
				if inheritDefaultProfiles {
					profileHID = generateProfileHumanID("default", profile)
					if !graph.HasNode(profileHID) {
						return fmt.Errorf("Instance %q depends on a non-existing profile (even with 'features.profiles=false') %q", inst.Name, profile)
					}
				} else {
					return fmt.Errorf("Instance %q depends on a non-existing profile %q", inst.Name, profile)
				}
			}

			profileHIDs = append(profileHIDs, profileHID)
		}

		// // Check if an instance depends on other entities through its local devices
		err := checkDevicesDependencies(projectName, projectHID, graph, localDevices, inst, instHID)
		if err != nil {
			return err
		}

		// connect its dependencies profiles to inst.
		for _, profileHID := range profileHIDs {
			if !graph.HasEdge(profileHID, instHID) {
				graph.SetEdge(profileHID, instHID)
			}
		}
	}

	return nil
}

func buildInstanceInventory(
	client lxd.InstanceServer,
	graph *DAG,
	defaultProjectHID string,
	otherProjectHIDs map[string]string,
	featuresPerProject map[string]map[string]string,
	nodeID *int64,
) error {
	// 1) First, process the 'profiles'
	profiles, err := client.GetProfiles()
	if err != nil {
		return err
	}

	err = addProfiles("default", defaultProjectHID, graph, profiles, nodeID)
	if err != nil {
		return err
	}

	for projectName, projectHID := range otherProjectHIDs {
		profiles, err := client.UseProject(projectName).GetProfiles()
		if err != nil {
			return err
		}

		if shared.IsTrue(featuresPerProject[projectName]["features.profiles"]) {
			err = addProfiles(projectName, projectHID, graph, profiles, nodeID)
			if err != nil {
				return err
			}
		} else {
			filteredProfiles := make([]api.Profile, 0)
			for _, profile := range profiles {
				profileHID := generateProfileHumanID(projectName, profile.Name)
				if !graph.HasNode(profileHID) {
					filteredProfiles = append(filteredProfiles, profile)
				}
			}

			err = addProfiles(projectName, projectHID, graph, filteredProfiles, nodeID)
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

	err = addInstances("default", defaultProjectHID, graph, append(containers, vms...), false, nodeID)
	if err != nil {
		return err
	}

	for projectName, projectHID := range otherProjectHIDs {
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

		err = addInstances(projectName, projectHID, graph, append(containers, vms...), inheritDefaultProfiles, nodeID)
		if err != nil {
			return err
		}
	}

	return nil
}

func buildResourcesInventory(client lxd.InstanceServer, l *Logger) (*DAG, error) {
	dag := NewDAG()
	nodeID := int64(0)

	// Get default project
	projects, err := client.GetProjects()
	if err != nil {
		return nil, err
	}

	if len(projects) == 0 {
		return nil, errors.New("No project found")
	}

	var defaultProjectHID string
	otherProjectHIDs := make(map[string]string)
	if len(projects) == 1 {
		// Only default project
		defaultProjectHID = generateProjectHumanID(projects[0].Name)
		dag.AddNode(nodeID, defaultProjectHID, projects[0])
		nodeID++

		l.Info("Default project found", map[string]any{"hid": defaultProjectHID, "project": projects[0]})
	} else {
		for _, project := range projects {
			projectHID := generateProjectHumanID(project.Name)
			dag.AddNode(nodeID, projectHID, project)
			if project.Name == "default" {
				defaultProjectHID = projectHID
			} else {
				otherProjectHIDs[project.Name] = projectHID
			}

			nodeID++
			l.Info("Project found", map[string]any{"hid": projectHID, "project": project})
		}
	}

	features := getFeaturesPerProject(projects)
	err = buildNetworkingInventory(client, dag, defaultProjectHID, otherProjectHIDs, features, &nodeID, l)
	if err != nil {
		return nil, err
	}

	err = buildStorageInventory(client, dag, otherProjectHIDs, features, &nodeID)
	if err != nil {
		return nil, err
	}

	err = buildInstanceInventory(client, dag, defaultProjectHID, otherProjectHIDs, features, &nodeID)
	if err != nil {
		return nil, err
	}

	return dag, nil
}
