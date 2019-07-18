package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
)

// APIHeartbeatMember contains specific cluster node info.
type APIHeartbeatMember struct {
	ID            int64     // ID field value in nodes table.
	Address       string    // Host and Port of node.
	RaftID        int64     // ID field value in raft_nodes table, zero if non-raft node.
	Raft          bool      // Deprecated, use non-zero RaftID instead to indicate raft node.
	LastHeartbeat time.Time // Last time we received a successful response from node.
	Online        bool      // Calculated from offline threshold and LastHeatbeat time.
	updated       bool      // Has node been updated during this heartbeat run. Not sent to nodes.
}

// APIHeartbeatVersion contains max versions for all nodes in cluster.
type APIHeartbeatVersion struct {
	Schema        int
	APIExtensions int
}

// APIHeartbeat contains data sent to nodes in heartbeat.
type APIHeartbeat struct {
	sync.Mutex // Used to control access to Members maps.
	Members    map[int64]APIHeartbeatMember
	Version    APIHeartbeatVersion
	Time       time.Time

	// Indicates if heartbeat contains a fresh set of node states.
	// This can be used to indicate to the receiving node that the state is fresh enough to
	// trigger node refresh activies (such as forkdns).
	FullStateList bool
}

// Update updates an existing APIHeartbeat struct with the raft and all node states supplied.
// If allNodes provided is an empty set then this is considered a non-full state list.
func (hbState *APIHeartbeat) Update(fullStateList bool, raftNodes []db.RaftNode, allNodes []db.NodeInfo, offlineThreshold time.Duration) {
	var maxSchemaVersion, maxAPIExtensionsVersion int
	hbState.Time = time.Now()

	if hbState.Members == nil {
		hbState.Members = make(map[int64]APIHeartbeatMember)
	}

	// If we've been supplied a fresh set of node states, this is a full state list.
	hbState.FullStateList = fullStateList

	raftNodeMap := make(map[string]db.RaftNode)

	// Convert raftNodes to a map keyed on address for lookups later.
	for _, raftNode := range raftNodes {
		raftNodeMap[raftNode.Address] = raftNode
	}

	// Add nodes (overwrites any nodes with same ID in map with fresh data).
	for _, node := range allNodes {
		member := APIHeartbeatMember{
			ID:            node.ID,
			Address:       node.Address,
			LastHeartbeat: node.Heartbeat,
			Online:        !node.Heartbeat.Before(time.Now().Add(-offlineThreshold)),
		}

		if raftNode, exists := raftNodeMap[member.Address]; exists {
			member.Raft = true // Deprecated
			member.RaftID = raftNode.ID
			delete(raftNodeMap, member.Address) // Used to check any remaining later.
		}

		// Add to the members map using the node ID (not the Raft Node ID).
		hbState.Members[node.ID] = member

		// Keep a record of highest APIExtensions and Schema version seen in all nodes.
		if node.APIExtensions > maxAPIExtensionsVersion {
			maxAPIExtensionsVersion = node.APIExtensions
		}

		if node.Schema > maxSchemaVersion {
			maxSchemaVersion = node.Schema
		}
	}

	hbState.Version = APIHeartbeatVersion{
		Schema:        maxSchemaVersion,
		APIExtensions: maxAPIExtensionsVersion,
	}

	if len(raftNodeMap) > 0 {
		logger.Errorf("Unaccounted raft node(s) not found in 'nodes' table for heartbeat: %+v", raftNodeMap)
	}

	return
}

// Send sends heartbeat requests to the nodes supplied and updates heartbeat state.
func (hbState *APIHeartbeat) Send(ctx context.Context, cert *shared.CertInfo, localAddress string, nodes []db.NodeInfo, delay bool) {
	heartbeatsWg := sync.WaitGroup{}
	sendHeartbeat := func(nodeID int64, address string, delay bool, heartbeatData *APIHeartbeat) {
		defer heartbeatsWg.Done()

		if delay {
			// Spread in time by waiting up to 3s less than the interval.
			time.Sleep(time.Duration(rand.Intn((heartbeatInterval*1000)-3000)) * time.Millisecond)
		}
		logger.Debugf("Sending heartbeat to %s", address)

		err := HeartbeatNode(ctx, address, cert, heartbeatData)

		if err == nil {
			hbState.Lock()
			// Ensure only update nodes that exist in Members already.
			hbNode, existing := hbState.Members[nodeID]
			if !existing {
				return
			}

			hbNode.LastHeartbeat = time.Now()
			hbNode.Online = true
			hbNode.updated = true
			hbState.Members[nodeID] = hbNode
			hbState.Unlock()
			logger.Debugf("Successful heartbeat for %s", address)
		} else {
			logger.Debugf("Failed heartbeat for %s: %v", address, err)
		}
	}

	for _, node := range nodes {
		// Special case for the local node - just record the time now.
		if node.Address == localAddress {
			hbState.Lock()
			hbNode := hbState.Members[node.ID]
			hbNode.LastHeartbeat = time.Now()
			hbNode.Online = true
			hbNode.updated = true
			hbState.Members[node.ID] = hbNode
			hbState.Unlock()
			continue
		}

		// Parallelize the rest.
		heartbeatsWg.Add(1)
		go sendHeartbeat(node.ID, node.Address, delay, hbState)
	}
	heartbeatsWg.Wait()
}

// HeartbeatTask returns a task function that performs leader-initiated heartbeat
// checks against all LXD nodes in the cluster.
//
// It will update the heartbeat timestamp column of the nodes table
// accordingly, and also notify them of the current list of database nodes.
func HeartbeatTask(gateway *Gateway) (task.Func, task.Schedule) {
	// Since the database APIs are blocking we need to wrap the core logic
	// and run it in a goroutine, so we can abort as soon as the context expires.
	heartbeatWrapper := func(ctx context.Context) {
		ch := make(chan struct{})
		go func() {
			gateway.heartbeat(ctx, false)
			ch <- struct{}{}
		}()
		select {
		case <-ch:
		case <-ctx.Done():
		}
	}

	schedule := task.Every(time.Duration(heartbeatInterval) * time.Second)

	return heartbeatWrapper, schedule
}

func (g *Gateway) heartbeat(ctx context.Context, initialHeartbeat bool) {
	if g.Cluster == nil || g.server == nil || g.memoryDial != nil {
		// We're not a raft node or we're not clustered
		return
	}

	raftNodes, err := g.currentRaftNodes()
	if err == errNotLeader {
		return
	}

	logger.Debugf("Starting heartbeat round")
	if err != nil {
		logger.Warnf("Failed to get current raft nodes: %v", err)
		return
	}

	// Replace the local raft_nodes table immediately because it
	// might miss a row containing ourselves, since we might have
	// been elected leader before the former leader had chance to
	// send us a fresh update through the heartbeat pool.
	logger.Debugf("Heartbeat updating local raft nodes to %+v", raftNodes)
	err = g.db.Transaction(func(tx *db.NodeTx) error {
		return tx.RaftNodesReplace(raftNodes)
	})
	if err != nil {
		logger.Warnf("Failed to replace local raft nodes: %v", err)
		return
	}

	var allNodes []db.NodeInfo
	var localAddress string // Address of this node
	var offlineThreshold time.Duration
	err = g.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		allNodes, err = tx.Nodes()
		if err != nil {
			return err
		}

		localAddress, err = tx.NodeAddress()
		if err != nil {
			return err
		}

		offlineThreshold, err = tx.NodeOfflineThreshold()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		logger.Warnf("Failed to get current cluster nodes: %v", err)
		return
	}

	// Cumulative set of node states (will be written back to database once done).
	hbState := &APIHeartbeat{}

	// If this leader node hasn't sent a heartbeat recently, then its node state records
	// are likely out of date, this can happen when a node becomes a leader.
	// Send stale set to all nodes in database to get a fresh set of active nodes.
	if initialHeartbeat {
		hbState.Update(false, raftNodes, allNodes, offlineThreshold)
		hbState.Send(ctx, g.cert, localAddress, allNodes, false)

		// We have the latest set of node states now, lets send that state set to all nodes.
		hbState.Update(true, raftNodes, allNodes, offlineThreshold)
		hbState.Send(ctx, g.cert, localAddress, allNodes, false)
	} else {
		hbState.Update(true, raftNodes, allNodes, offlineThreshold)
		hbState.Send(ctx, g.cert, localAddress, allNodes, true)
	}

	// Look for any new node which appeared since sending last heartbeat.
	var currentNodes []db.NodeInfo
	err = g.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		currentNodes, err = tx.Nodes()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		logger.Warnf("Failed to get current cluster nodes: %v", err)
		return
	}

	newNodes := []db.NodeInfo{}
	for _, currentNode := range currentNodes {
		existing := false
		for _, node := range allNodes {
			if node.Address == currentNode.Address && node.ID == currentNode.ID {
				existing = true
				break
			}
		}

		if !existing {
			// We found a new node
			allNodes = append(allNodes, currentNode)
			newNodes = append(newNodes, currentNode)
		}
	}

	// If any new nodes found, send heartbeat to just them (with full node state).
	if len(newNodes) > 0 {
		hbState.Update(true, raftNodes, allNodes, offlineThreshold)
		hbState.Send(ctx, g.cert, localAddress, newNodes, false)
	}

	// If the context has been cancelled, return immediately.
	if ctx.Err() != nil {
		logger.Debugf("Aborting heartbeat round")
		return
	}

	err = g.Cluster.Transaction(func(tx *db.ClusterTx) error {
		for _, node := range hbState.Members {
			if !node.updated {
				continue
			}

			err := tx.NodeHeartbeat(node.Address, time.Now())
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		logger.Warnf("Failed to update heartbeat: %v", err)
	}

	// If full node state was sent and node refresh task is specified, run it async.
	if g.HeartbeatNodeHook != nil {
		go g.HeartbeatNodeHook(hbState)
	}

	// Update last leader heartbeat time so next time a full node state list can be sent (if not this time).
	logger.Debugf("Completed heartbeat round")
}

// heartbeatInterval Number of seconds to wait between to heartbeat rounds.
const heartbeatInterval = 10

// HeartbeatNode performs a single heartbeat request against the node with the given address.
func HeartbeatNode(taskCtx context.Context, address string, cert *shared.CertInfo, heartbeatData *APIHeartbeat) error {
	logger.Debugf("Sending heartbeat request to %s", address)

	config, err := tlsClientConfig(cert)
	if err != nil {
		return err
	}

	timeout := 2 * time.Second
	url := fmt.Sprintf("https://%s%s", address, databaseEndpoint)
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: config},
		Timeout:   timeout,
	}

	buffer := bytes.Buffer{}
	err = json.NewEncoder(&buffer).Encode(heartbeatData)
	if err != nil {
		return err
	}

	request, err := http.NewRequest("PUT", url, bytes.NewReader(buffer.Bytes()))
	if err != nil {
		return err
	}
	setDqliteVersionHeader(request)

	// Use 1s later timeout to give HTTP client chance timeout with more useful info.
	ctx, cancel := context.WithTimeout(context.Background(), timeout+time.Second)
	defer cancel()
	request = request.WithContext(ctx)
	request.Close = true // Immediately close the connection after the request is done

	response, err := client.Do(request)
	if err != nil {
		return errors.Wrap(err, "failed to send HTTP request")
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP request failed: %s", response.Status)
	}

	return nil
}
