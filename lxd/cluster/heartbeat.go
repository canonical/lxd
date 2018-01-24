package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/raft"
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
		logger.Debugf("Starting heartbeat round")

		raftNodes, err := gateway.currentRaftNodes()
		if err == raft.ErrNotLeader {
			return
		}
		if err != nil {
			logger.Warnf("Failed to get current raft nodes: %v", err)
			return
		}
		var nodes []db.NodeInfo
		err = cluster.Transaction(func(tx *db.ClusterTx) error {
			var err error
			nodes, err = tx.Nodes()
			return err
		})
		if err != nil {
			logger.Warnf("Failed to get current cluster nodes: %v", err)
			return
		}
		wg := sync.WaitGroup{}
		wg.Add(len(nodes))
		heartbeats := make([]time.Time, len(nodes))
		for i, node := range nodes {
			go func(i int, address string) {
				defer wg.Done()
				err := heartbeatNode(ctx, address, gateway.cert, raftNodes)
				if err == nil {
					logger.Debugf("Successful heartbeat for %s", address)
					heartbeats[i] = time.Now()
				} else {
					logger.Debugf("Failed heartbeat for %s: %v", address, err)
				}
			}(i, node.Address)
		}
		wg.Wait()

		// If the context has been cancelled, return immediately.
		if ctx.Err() != nil {
			return
		}

		err = cluster.Transaction(func(tx *db.ClusterTx) error {
			for i, node := range nodes {
				if heartbeats[i].Equal(time.Time{}) {
					continue
				}
				err := tx.NodeHeartbeat(node.Address, heartbeats[i])
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			logger.Warnf("Failed to update heartbeat: %v", err)
		}
	}

	schedule := task.Every(time.Duration(heartbeatInterval) * time.Second)

	return heartbeat, schedule
}

// Number of seconds to wait between to heartbeat rounds.
const heartbeatInterval = 3

// Perform a single heartbeat request against the node with the given address.
func heartbeatNode(ctx context.Context, address string, cert *shared.CertInfo, raftNodes []db.RaftNode) error {
	config, err := tlsClientConfig(cert)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://%s%s", address, grpcEndpoint)
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
