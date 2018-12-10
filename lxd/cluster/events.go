package cluster

import (
	"time"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/endpoints"
	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"golang.org/x/net/context"
)

// Events starts a task that continuously monitors the list of cluster nodes and
// maintains a pool of websocket connections against all of them, in order to
// get notified about events.
//
// Whenever an event is received the given callback is invoked.
func Events(endpoints *endpoints.Endpoints, cluster *db.Cluster, f func(int64, api.Event)) (task.Func, task.Schedule) {
	listeners := map[int64]*lxd.EventListener{}

	// Update our pool of event listeners. Since database queries are
	// blocking, we spawn the actual logic in a goroutine, to abort
	// immediately when we receive the stop signal.
	update := func(ctx context.Context) {
		ch := make(chan struct{})
		go func() {
			eventsUpdateListeners(endpoints, cluster, listeners, f)
			ch <- struct{}{}
		}()
		select {
		case <-ch:
		case <-ctx.Done():
		}

	}

	schedule := task.Every(time.Second)

	return update, schedule
}

func eventsUpdateListeners(endpoints *endpoints.Endpoints, cluster *db.Cluster, listeners map[int64]*lxd.EventListener, f func(int64, api.Event)) {
	// Get the current cluster nodes.
	var nodes []db.NodeInfo
	var offlineThreshold time.Duration

	err := cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		nodes, err = tx.Nodes()
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
	if len(nodes) == 1 {
		return // Either we're not clustered or this is a single-node cluster
	}

	address := endpoints.NetworkAddress()

	ids := make([]int, len(nodes))
	for i, node := range nodes {
		ids[i] = int(node.ID)

		// Don't bother trying to connect to offline nodes, or to ourselves.
		if node.IsOffline(offlineThreshold) || node.Address == address {
			continue
		}

		_, ok := listeners[node.ID]

		// The node has already a listener associated to it.
		if ok {
			// Double check that the listener is still
			// connected. If it is, just move on, other
			// we'll try to connect again.
			if listeners[node.ID].IsActive() {
				continue
			}
			delete(listeners, node.ID)
		}

		listener, err := eventsConnect(node.Address, endpoints.NetworkCert())
		if err != nil {
			logger.Warnf("Failed to get events from node %s: %v", node.Address, err)
			continue
		}
		logger.Debugf("Listening for events on node %s", node.Address)
		listener.AddHandler(nil, func(event api.Event) { f(node.ID, event) })
		listeners[node.ID] = listener
	}
	for id, listener := range listeners {
		if !shared.IntInSlice(int(id), ids) {
			listener.Disconnect()
			delete(listeners, id)
		}
	}
}

// Establish a client connection to get events from the given node.
func eventsConnect(address string, cert *shared.CertInfo) (*lxd.EventListener, error) {
	client, err := Connect(address, cert, true)
	if err != nil {
		return nil, err
	}

	// Set the project to the special wildcard in order to get notified
	// about all events across all projects.
	client = client.UseProject("*")

	return client.GetEvents()
}
