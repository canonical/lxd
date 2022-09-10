package cluster

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

// Notifier is a function that invokes the given function against each node in
// the cluster excluding the invoking one.
type Notifier func(hook func(lxd.InstanceServer) error) error

// NotifierPolicy can be used to tweak the behavior of NewNotifier in case of
// some nodes are down.
type NotifierPolicy int

// Possible notifcation policies.
const (
	NotifyAll    NotifierPolicy = iota // Requires that all nodes are up.
	NotifyAlive                        // Only notifies nodes that are alive
	NotifyTryAll                       // Attempt to notify all nodes regardless of state.
)

// NewNotifier builds a Notifier that can be used to notify other peers using
// the given policy.
func NewNotifier(state *state.State, networkCert *shared.CertInfo, serverCert *shared.CertInfo, policy NotifierPolicy) (Notifier, error) {
	address, err := node.ClusterAddress(state.DB.Node)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch node address: %w", err)
	}

	// Fast-track the case where we're not clustered at all.
	if address == "" {
		nullNotifier := func(func(lxd.InstanceServer) error) error { return nil }
		return nullNotifier, nil
	}

	var nodes []db.NodeInfo
	var offlineThreshold time.Duration
	err = state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		offlineThreshold, err = tx.GetNodeOfflineThreshold(ctx)
		if err != nil {
			return err
		}

		nodes, err = tx.GetNodes(ctx)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	peers := []string{}
	for _, node := range nodes {
		if node.Address == address || node.Address == "0.0.0.0" {
			continue // Exclude ourselves
		}

		if node.IsOffline(offlineThreshold) {
			// Even if the heartbeat timestamp is not recent
			// enough, let's try to connect to the node, just in
			// case the heartbeat is lagging behind for some reason
			// and the node is actually up.
			if !HasConnectivity(networkCert, serverCert, node.Address) {
				switch policy {
				case NotifyAll:
					return nil, fmt.Errorf("peer node %s is down", node.Address)
				case NotifyAlive:
					continue // Just skip this node
				case NotifyTryAll:
				}
			}
		}

		peers = append(peers, node.Address)
	}

	notifier := func(hook func(lxd.InstanceServer) error) error {
		errs := make([]error, len(peers))
		wg := sync.WaitGroup{}
		wg.Add(len(peers))
		for i, address := range peers {
			logger.Debugf("Notify node %s of state changes", address)
			go func(i int, address string) {
				defer wg.Done()
				client, err := Connect(address, networkCert, serverCert, nil, true)
				if err != nil {
					errs[i] = fmt.Errorf("failed to connect to peer %s: %w", address, err)
					return
				}

				err = hook(client)
				if err != nil {
					errs[i] = fmt.Errorf("failed to notify peer %s: %w", address, err)
				}
			}(i, address)
		}

		wg.Wait()
		// TODO: aggregate all errors?
		for i, err := range errs {
			if err != nil {
				if shared.IsConnectionError(err) && policy == NotifyAlive {
					logger.Warnf("Could not notify node %s", peers[i])
					continue
				}

				return err
			}
		}
		return nil
	}

	return notifier, nil
}
