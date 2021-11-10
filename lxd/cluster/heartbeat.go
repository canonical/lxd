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
	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/query"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/lxd/warnings"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
)

type heartbeatMode int

const (
	// HearbeatNormal normal heartbeat.
	HearbeatNormal heartbeatMode = iota

	// HearbeatImmediate immediate heartbeat.
	HearbeatImmediate

	// HearbeatInitial initial heartbeat.
	HearbeatInitial
)

// APIHeartbeatMember contains specific cluster node info.
type APIHeartbeatMember struct {
	ID            int64     // ID field value in nodes table.
	Address       string    // Host and Port of node.
	Name          string    // Name of cluster member.
	RaftID        uint64    // ID field value in raft_nodes table, zero if non-raft node.
	RaftRole      int       // Node role in the raft cluster, from the raft_nodes table
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
	cluster    *db.Cluster
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

	if hbState.Members == nil {
		hbState.Members = make(map[int64]APIHeartbeatMember)
	}

	// If we've been supplied a fresh set of node states, this is a full state list.
	hbState.FullStateList = fullStateList

	// Convert raftNodes to a map keyed on address for lookups later.
	raftNodeMap := make(map[string]db.RaftNode, len(raftNodes))
	for _, raftNode := range raftNodes {
		raftNodeMap[raftNode.Address] = raftNode
	}

	// Add nodes (overwrites any nodes with same ID in map with fresh data).
	for _, node := range allNodes {
		member := APIHeartbeatMember{
			ID:            node.ID,
			Address:       node.Address,
			Name:          node.Name,
			LastHeartbeat: node.Heartbeat,
			Online:        !node.Heartbeat.Before(time.Now().Add(-offlineThreshold)),
		}

		if raftNode, exists := raftNodeMap[member.Address]; exists {
			member.Raft = true // Deprecated
			member.RaftID = raftNode.ID
			member.RaftRole = int(raftNode.Role)
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

	if len(raftNodeMap) > 0 && hbState.cluster != nil {
		hbState.cluster.Transaction(func(tx *db.ClusterTx) error {
			for addr, raftNode := range raftNodeMap {
				_, err := tx.GetPendingNodeByAddress(addr)
				if err != nil {
					logger.Errorf("Unaccounted raft node(s) not found in 'nodes' table for heartbeat: %+v", raftNode)
				}
			}

			return nil
		})
	}

	return
}

// Send sends heartbeat requests to the nodes supplied and updates heartbeat state.
func (hbState *APIHeartbeat) Send(ctx context.Context, networkCert *shared.CertInfo, serverCert *shared.CertInfo, localAddress string, nodes []db.NodeInfo, spreadDuration time.Duration) {
	heartbeatsWg := sync.WaitGroup{}
	sendHeartbeat := func(nodeID int64, address string, spreadDuration time.Duration, heartbeatData *APIHeartbeat) {
		defer heartbeatsWg.Done()

		if spreadDuration > 0 {
			// Spread in time by waiting up to 3s less than the interval.
			spreadDurationMs := int(spreadDuration.Milliseconds())
			spreadRange := spreadDurationMs - 3000

			if spreadRange > 0 {
				time.Sleep(time.Duration(rand.Intn(spreadRange)) * time.Millisecond)
			}
		}

		// Update timestamp to current, used for time skew detection
		heartbeatData.Time = time.Now().UTC()

		hbCtx, hbCtxCancel := context.WithTimeout(ctx, time.Second*3)
		err := HeartbeatNode(hbCtx, address, networkCert, serverCert, heartbeatData)
		defer hbCtxCancel()
		if err == nil {
			heartbeatData.Lock()
			// Ensure only update nodes that exist in Members already.
			hbNode, existing := hbState.Members[nodeID]
			if !existing {
				return
			}

			hbNode.LastHeartbeat = time.Now()
			hbNode.Online = true
			hbNode.updated = true
			heartbeatData.Members[nodeID] = hbNode
			heartbeatData.Unlock()
			logger.Debug("Successful heartbeat", log.Ctx{"address": address})

			err = warnings.ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(hbState.cluster, "", db.WarningOfflineClusterMember, cluster.TypeNode, int(nodeID))
			if err != nil {
				logger.Warn("Failed to resolve warning", log.Ctx{"err": err})
			}
		} else {
			logger.Warn("Failed heartbeat", log.Ctx{"address": address, "err": err})

			err = hbState.cluster.UpsertWarningLocalNode("", cluster.TypeNode, int(nodeID), db.WarningOfflineClusterMember, err.Error())
			if err != nil {
				logger.Warn("Failed to create warning", log.Ctx{"err": err})
			}
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
		go sendHeartbeat(node.ID, node.Address, spreadDuration, hbState)
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
			gateway.heartbeat(HearbeatNormal)
			ch <- struct{}{}
		}()
		select {
		case <-ch:
		case <-ctx.Done():
		}
	}

	schedule := func() (time.Duration, error) {
		return task.Every(gateway.heartbeatInterval())()
	}

	return heartbeatWrapper, schedule
}

// heartbeatInterval returns heartbeat interval to use.
func (g *Gateway) heartbeatInterval() time.Duration {
	threshold := g.HeartbeatOfflineThreshold
	if threshold <= 0 {
		threshold = time.Duration(db.DefaultOfflineThreshold) * time.Second
	}

	return threshold / 2
}

// HeartbeatRestart restarts cancels any ongoing heartbeat and restarts it.
// If there is no ongoing heartbeat then this is a no-op.
// Returns true if new heartbeat round was started.
func (g *Gateway) HeartbeatRestart(forceStart bool, mode heartbeatMode) bool {
	g.heartbeatCancelLock.Lock() // Make sure we're the only ones inspecting the g.heartbeatCancel var.

	// There is a cancellable heartbeat round ongoing.
	if g.heartbeatCancel != nil {
		g.heartbeatCancel()            // Request ongoing hearbeat round cancel itself.
		g.heartbeatCancel = nil        // Indicate there is no further cancellable heartbeat round.
		g.heartbeatCancelLock.Unlock() // Release lock ready for g.heartbeat to acquire it.

		// Start a new heartbeat round async that will run as soon as ongoing heartbeat round exits.
		g.heartbeat(mode)

		return true
	}

	// No cancellable heartbeat round, release lock.
	g.heartbeatCancelLock.Unlock()

	if forceStart {
		g.heartbeat(mode)

		return true
	}

	return false
}

func (g *Gateway) heartbeat(mode heartbeatMode) {
	// Avoid concurent heartbeat loops.
	// This is possible when both the regular task and the out of band heartbeat round from a dqlite
	// connection or notification restart both kick in at the same time.
	g.heartbeatLock.Lock()
	defer g.heartbeatLock.Unlock()

	// Acquire the cancellation lock and populate it so that this heartbeat round can be cancelled if a
	// notification cancellation request arrives during the round. Also setup a defer so that the cancellation
	// function is set to nil when this function ends to indicate there is no ongoing heartbeat round.
	g.heartbeatCancelLock.Lock()
	var ctx context.Context
	ctx, g.heartbeatCancel = context.WithCancel(g.Context())
	defer func() {
		g.heartbeatCancelLock.Lock()
		if g.heartbeatCancel != nil {
			g.heartbeatCancel()
			g.heartbeatCancel = nil
		}
		g.heartbeatCancelLock.Unlock()
	}()
	g.heartbeatCancelLock.Unlock()

	if g.Cluster == nil || g.server == nil || g.memoryDial != nil {
		// We're not a raft node or we're not clustered
		return
	}

	raftNodes, err := g.currentRaftNodes()
	if err != nil {
		if errors.Cause(err) == ErrNotLeader {
			return
		}

		logger.Error("Failed to get current raft members", log.Ctx{"err": err})
		return
	}

	// Address of this node.
	localAddress, err := node.ClusterAddress(g.db)
	if err != nil {
		logger.Error("Failed to fetch local cluster address", log.Ctx{"err": err})
	}

	var allNodes []db.NodeInfo
	err = g.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		allNodes, err = tx.GetNodes()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		logger.Warn("Failed to get current cluster members", log.Ctx{"err": err})
		return
	}

	modeStr := "normal"
	switch mode {
	case HearbeatImmediate:
		modeStr = "immediate"
	case HearbeatInitial:
		modeStr = "initial"
	}

	if mode != HearbeatNormal {
		// Log unscheduled heartbeats with a higher level than normal heartbeats.
		logger.Info("Starting heartbeat round", log.Ctx{"mode": modeStr, "address": localAddress})
	} else {
		// Don't spam the normal log with regular heartbeat messages.
		logger.Debug("Starting heartbeat round", log.Ctx{"mode": modeStr, "address": localAddress})
	}

	// Replace the local raft_nodes table immediately because it
	// might miss a row containing ourselves, since we might have
	// been elected leader before the former leader had chance to
	// send us a fresh update through the heartbeat pool.
	logger.Debug("Heartbeat updating local raft members", log.Ctx{"members": raftNodes})
	err = g.db.Transaction(func(tx *db.NodeTx) error {
		return tx.ReplaceRaftNodes(raftNodes)
	})
	if err != nil {
		logger.Warn("Failed to replace local raft members", log.Ctx{"err": err})
		return
	}

	if localAddress == "" {
		logger.Warn("No local address set, aborting heartbeat round")
		return
	}

	startTime := time.Now()

	heartbeatInterval := g.heartbeatInterval()

	// Cumulative set of node states (will be written back to database once done).
	hbState := &APIHeartbeat{cluster: g.Cluster}

	// If we are doing a normal heartbeat round then spread the requests over the heartbeatInterval in order
	// to reduce load on the cluster.
	spreadDuration := time.Duration(0)
	if mode == HearbeatNormal {
		spreadDuration = heartbeatInterval
	}

	// If this leader node hasn't sent a heartbeat recently, then its node state records
	// are likely out of date, this can happen when a node becomes a leader.
	// Send stale set to all nodes in database to get a fresh set of active nodes.
	if mode == HearbeatInitial {
		hbState.Update(false, raftNodes, allNodes, g.HeartbeatOfflineThreshold)
		hbState.Send(ctx, g.networkCert, g.serverCert(), localAddress, allNodes, spreadDuration)

		// We have the latest set of node states now, lets send that state set to all nodes.
		hbState.FullStateList = true
		hbState.Send(ctx, g.networkCert, g.serverCert(), localAddress, allNodes, spreadDuration)
	} else {
		hbState.Update(true, raftNodes, allNodes, g.HeartbeatOfflineThreshold)
		hbState.Send(ctx, g.networkCert, g.serverCert(), localAddress, allNodes, spreadDuration)
	}

	// Look for any new node which appeared since sending last heartbeat.
	var currentNodes []db.NodeInfo
	err = g.Cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		currentNodes, err = tx.GetNodes()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		logger.Warn("Failed to get current cluster members", log.Ctx{"err": err})
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
		hbState.Update(true, raftNodes, allNodes, g.HeartbeatOfflineThreshold)
		hbState.Send(ctx, g.networkCert, g.serverCert(), localAddress, newNodes, 0)
	}

	// If the context has been cancelled, return immediately.
	err = ctx.Err()
	if err != nil {
		logger.Warn("Aborting heartbeat round", log.Ctx{"err": err})
		return
	}

	var unavailableMembers []string

	err = query.Retry(func() error {
		return g.Cluster.Transaction(func(tx *db.ClusterTx) error {
			for _, node := range hbState.Members {
				if !node.updated {
					// If member has not been updated during this heartbeat round it means
					// they are currently unreachable or rejecting heartbeats due to being
					// in the process of shutting down. Eitherway we do not want to use this
					// member as a candidate for role promotion.
					unavailableMembers = append(unavailableMembers, node.Address)
					continue
				}

				err := tx.SetNodeHeartbeat(node.Address, node.LastHeartbeat)
				if err != nil && errors.Cause(err) != db.ErrNoSuchObject {
					return errors.Wrapf(err, "Failed updating heartbeat time for member %q", node.Address)
				}
			}

			return nil
		})
	})
	if err != nil {
		logger.Error("Failed updating cluster heartbeats", log.Ctx{"err": err})
		return
	}

	// If full node state was sent and node refresh task is specified, run it async.
	g.HeartbeatNodeHook(hbState, true, unavailableMembers)

	duration := time.Now().Sub(startTime)
	if duration > heartbeatInterval {
		logger.Warn("Heartbeat round duration greater than heartbeat interval", log.Ctx{"duration": duration, "interval": heartbeatInterval})
	}

	// Update last leader heartbeat time so next time a full node state list can be sent (if not this time).
	logger.Debug("Completed heartbeat round", log.Ctx{"duration": duration, "address": localAddress})
}

// HeartbeatNode performs a single heartbeat request against the node with the given address.
func HeartbeatNode(ctx context.Context, address string, networkCert *shared.CertInfo, serverCert *shared.CertInfo, heartbeatData *APIHeartbeat) error {
	logger.Debug("Sending heartbeat request", log.Ctx{"address": address})

	config, err := tlsClientConfig(networkCert, serverCert)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://%s%s", address, databaseEndpoint)
	transport, cleanup := tlsTransport(config)
	defer cleanup()
	client := &http.Client{
		Transport: transport,
	}

	buffer := bytes.Buffer{}
	heartbeatData.Lock()
	err = json.NewEncoder(&buffer).Encode(heartbeatData)
	heartbeatData.Unlock()
	if err != nil {
		return err
	}

	request, err := http.NewRequest("PUT", url, bytes.NewReader(buffer.Bytes()))
	if err != nil {
		return err
	}
	setDqliteVersionHeader(request)

	request = request.WithContext(ctx)
	request.Close = true // Immediately close the connection after the request is done

	response, err := client.Do(request)
	if err != nil {
		return errors.Wrap(err, "Failed to send heartbeat request")
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Heartbeat request failed with status: %w", api.StatusErrorf(response.StatusCode, response.Status))
	}

	return nil
}
