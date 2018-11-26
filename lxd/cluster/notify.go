package cluster

import (
	"fmt"
	"strings"
	"sync"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/node"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
)

// Notifier is a function that invokes the given function against each node in
// the cluster excluding the invoking one.
type Notifier func(hook func(lxd.ContainerServer) error) error

// NotifierPolicy can be used to tweak the behavior of NewNotifier in case of
// some nodes are down.
type NotifierPolicy int

// Possible notifcation policies.
const (
	NotifyAll   NotifierPolicy = iota // Requires that all nodes are up.
	NotifyAlive                       // Only notifies nodes that are alive
)

// NewNotifier builds a Notifier that can be used to notify other peers using
// the given policy.
func NewNotifier(state *state.State, cert *shared.CertInfo, policy NotifierPolicy) (Notifier, error) {
	address, err := node.ClusterAddress(state.Node)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch node address")
	}

	// Fast-track the case where we're not clustered at all.
	if address == "" {
		nullNotifier := func(func(lxd.ContainerServer) error) error { return nil }
		return nullNotifier, nil
	}

	peers := []string{}
	err = state.Cluster.Transaction(func(tx *db.ClusterTx) error {
		offlineThreshold, err := tx.NodeOfflineThreshold()
		if err != nil {
			return err
		}

		nodes, err := tx.Nodes()
		if err != nil {
			return err
		}
		for _, node := range nodes {
			if node.Address == address || node.Address == "0.0.0.0" {
				continue // Exclude ourselves
			}
			if node.IsOffline(offlineThreshold) {
				switch policy {
				case NotifyAll:
					return fmt.Errorf("peer node %s is down", node.Address)
				case NotifyAlive:
					continue // Just skip this node
				}
			}
			peers = append(peers, node.Address)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	notifier := func(hook func(lxd.ContainerServer) error) error {
		errs := make([]error, len(peers))
		wg := sync.WaitGroup{}
		wg.Add(len(peers))
		for i, address := range peers {
			logger.Debugf("Notify node %s of state changes", address)
			go func(i int, address string) {
				defer wg.Done()
				client, err := Connect(address, cert, true)
				if err != nil {
					errs[i] = errors.Wrapf(err, "failed to connect to peer %s", address)
					return
				}
				err = hook(client)
				if err != nil {
					errs[i] = errors.Wrapf(err, "failed to notify peer %s", address)
				}
			}(i, address)
		}
		wg.Wait()
		// TODO: aggregate all errors?
		for i, err := range errs {
			if err != nil {
				// FIXME: unfortunately the LXD client currently does not
				//        provide a way to differentiate between errors
				if isClientConnectionError(err) && policy == NotifyAlive {
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

// Return true if the given error is due to the LXD Go client not being able to
// connect to the target LXD node.
func isClientConnectionError(err error) bool {
	// FIXME: unfortunately the LXD client currently does not
	//        provide a way to differentiate between errors
	return strings.Contains(err.Error(), "Unable to connect to")
}
