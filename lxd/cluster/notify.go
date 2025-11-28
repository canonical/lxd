package cluster

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// Notifier is a function that invokes `hook` against each node in the cluster,
// excluding the invoking one. The NodeInfo passed to `hook` describes the
// cluster member of InstanceServer.
type Notifier func(hook func(db.NodeInfo, lxd.InstanceServer) error) error

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
func NewNotifier(state *state.State, networkCert *shared.CertInfo, serverCert *shared.CertInfo, policy NotifierPolicy, members ...db.NodeInfo) (Notifier, error) {
	localClusterAddress := state.LocalConfig.ClusterAddress()

	// Unfortunately the notifier is called during database startup before the
	// global config has been loaded, so we need to fall back on loading the
	// offline threshold from the database.
	var offlineThreshold time.Duration
	if state.GlobalConfig != nil {
		offlineThreshold = state.GlobalConfig.OfflineThreshold()
	}

	// Fast-track the case where we're not clustered at all.
	if !state.ServerClustered {
		nullNotifier := func(func(db.NodeInfo, lxd.InstanceServer) error) error { return nil }
		return nullNotifier, nil
	}

	if len(members) == 0 || state.GlobalConfig == nil {
		err := state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error
			if state.GlobalConfig == nil {
				offlineThreshold, err = tx.GetNodeOfflineThreshold(ctx)
				if err != nil {
					return fmt.Errorf("Failed getting cluster offline threshold: %w", err)
				}
			}

			if len(members) == 0 {
				members, err = tx.GetNodes(ctx)
				if err != nil {
					return fmt.Errorf("Failed getting cluster members: %w", err)
				}
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// Filter out ourselves and nodes that are offline.
	peers := make([]db.NodeInfo, 0, len(members)-1)
	for _, member := range members {
		if member.Address == localClusterAddress || member.Address == "0.0.0.0" {
			continue // Exclude ourselves
		}

		if member.IsOffline(offlineThreshold) {
			// Even if the heartbeat timestamp is not recent
			// enough, let's try to connect to the node, just in
			// case the heartbeat is lagging behind for some reason
			// and the node is actually up.
			if !HasConnectivity(networkCert, serverCert, member.Address) {
				switch policy {
				case NotifyAll:
					return nil, fmt.Errorf("Peer cluster member %s at %s is down", member.Name, member.Address)
				case NotifyAlive:
					continue // Just skip this node
				case NotifyTryAll:
				}
			}
		}

		peers = append(peers, member)
	}

	notifier := func(hook func(db.NodeInfo, lxd.InstanceServer) error) error {
		errs := make([]error, len(peers))
		wg := sync.WaitGroup{}
		wg.Add(len(peers))
		for i, member := range peers {
			logger.Debug("Notify cluster member of state changes", logger.Ctx{"name": member.Name, "address": member.Address})
			go func(i int, member db.NodeInfo) {
				defer wg.Done()
				client, err := Connect(context.Background(), member.Address, networkCert, serverCert, true)
				if err != nil {
					errs[i] = fmt.Errorf("Failed connecting to cluster member %q at %q: %w", member.Name, member.Address, err)
					return
				}

				err = hook(member, client)
				if err != nil {
					errs[i] = fmt.Errorf("Failed notifying cluster member %q at %q: %w", member.Name, member.Address, err)
				}
			}(i, member)
		}

		wg.Wait()
		// TODO: aggregate all errors?
		for i, err := range errs {
			if err != nil {
				isDown := shared.IsConnectionError(err) || api.StatusErrorCheck(err, http.StatusServiceUnavailable)

				if isDown && policy == NotifyAlive {
					logger.Warn("Could not notify cluster member", logger.Ctx{"name": peers[i].Name, "address": peers[i].Address})
					continue
				}

				return err
			}
		}

		return nil
	}

	return notifier, nil
}
