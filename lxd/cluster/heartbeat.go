package cluster

import (
	"bytes"
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
	"golang.org/x/net/context"
)

// Heartbeat returns a task function that performs leader-initiated heartbeat
// checks against all LXD nodes in the cluster.
//
// It will update the heartbeat timestamp column of the nodes table
// accordingly, and also notify them of the current list of database nodes.
func Heartbeat(gateway *Gateway, cluster *db.Cluster) (task.Func, task.Schedule) {
	heartbeat := func(ctx context.Context) {
		if gateway.server == nil || gateway.memoryDial != nil {
			// We're not a raft node or we're not clustered
			return
		}

		raftNodes, err := gateway.currentRaftNodes()
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
		err = gateway.db.Transaction(func(tx *db.NodeTx) error {
			return tx.RaftNodesReplace(raftNodes)
		})
		if err != nil {
			logger.Warnf("Failed to replace local raft nodes: %v", err)
			return
		}

		var nodes []db.NodeInfo
		var nodeAddress string // Address of this node
		err = cluster.Transaction(func(tx *db.ClusterTx) error {
			var err error
			nodes, err = tx.Nodes()
			if err != nil {
				return err
			}

			nodeAddress, err = tx.NodeAddress()
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			logger.Warnf("Failed to get current cluster nodes: %v", err)
			return
		}

		heartbeats := map[int64]bool{}
		heartbeatsLock := sync.Mutex{}
		heartbeatsWg := sync.WaitGroup{}

		sendHeartbeat := func(id int64, address string, delay bool) {
			defer heartbeatsWg.Done()

			if delay {
				// Spread in time by waiting up to 3s less than the interval
				time.Sleep(time.Duration(rand.Intn((heartbeatInterval*1000)-3000)) * time.Millisecond)
			}
			logger.Debugf("Sending heartbeat to %s", address)

			err := heartbeatNode(ctx, address, gateway.cert, raftNodes)
			if err == nil {
				heartbeatsLock.Lock()
				heartbeats[id] = true
				heartbeatsLock.Unlock()
				logger.Debugf("Successful heartbeat for %s", address)
			} else {
				logger.Debugf("Failed heartbeat for %s: %v", address, err)
			}
		}

		for _, node := range nodes {
			// Special case the local node
			if node.Address == nodeAddress {
				heartbeatsLock.Lock()
				heartbeats[node.ID] = true
				heartbeatsLock.Unlock()
				continue
			}

			// Parallelize the rest
			heartbeatsWg.Add(1)
			go sendHeartbeat(node.ID, node.Address, true)
		}
		heartbeatsWg.Wait()

		// Look for any new node which appeared since
		var currentNodes []db.NodeInfo
		err = cluster.Transaction(func(tx *db.ClusterTx) error {
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

		for _, currentNode := range currentNodes {
			found := false
			for _, node := range nodes {
				if node.Address == currentNode.Address {
					found = true
					break
				}
			}

			if !found {
				// We found a new node
				nodes = append(nodes, currentNode)
				heartbeatsWg.Add(1)
				go sendHeartbeat(currentNode.ID, currentNode.Address, false)
			}
		}
		heartbeatsWg.Wait()

		// If the context has been cancelled, return immediately.
		if ctx.Err() != nil {
			logger.Debugf("Aborting heartbeat round")
			return
		}

		err = cluster.Transaction(func(tx *db.ClusterTx) error {
			for _, node := range nodes {
				if !heartbeats[node.ID] {
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
		logger.Debugf("Completed heartbeat round")
	}

	// Since the database APIs are blocking we need to wrap the core logic
	// and run it in a goroutine, so we can abort as soon as the context expires.
	heartbeatWrapper := func(ctx context.Context) {
		ch := make(chan struct{})
		go func() {
			heartbeat(ctx)
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

// Number of seconds to wait between to heartbeat rounds.
const heartbeatInterval = 10

// Perform a single heartbeat request against the node with the given address.
func heartbeatNode(taskCtx context.Context, address string, cert *shared.CertInfo, raftNodes []db.RaftNode) error {
	logger.Debugf("Sending heartbeat request to %s", address)

	config, err := tlsClientConfig(cert)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://%s%s", address, databaseEndpoint)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: config}}

	buffer := bytes.Buffer{}
	err = json.NewEncoder(&buffer).Encode(raftNodes)
	if err != nil {
		return err
	}

	request, err := http.NewRequest("PUT", url, bytes.NewReader(buffer.Bytes()))
	if err != nil {
		return err
	}
	setDqliteVersionHeader(request)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request = request.WithContext(ctx)
	request.Close = true // Immediately close the connection after the request is done

	// Perform the request asynchronously, so we can abort it if the task context is done.
	errCh := make(chan error)
	go func() {
		response, err := client.Do(request)
		if err != nil {
			errCh <- errors.Wrap(err, "failed to send HTTP request")
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			errCh <- fmt.Errorf("HTTP request failed: %s", response.Status)
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-taskCtx.Done():
		return taskCtx.Err()
	}
}
