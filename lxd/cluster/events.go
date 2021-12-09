package cluster

import (
	"context"
	"sync"
	"time"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/endpoints"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

var listeners = map[string]*lxd.EventListener{}
var listenersNotify = map[string]chan struct{}{}
var listenersLock sync.Mutex

// EventListenerWait waits for there to be listener connected to the specified address.
func EventListenerWait(ctx context.Context, address string) (*lxd.EventListener, error) {
	// Check if there is already a listener.
	listenersLock.Lock()
	listener, found := listeners[address]
	if found && listener.IsActive() {
		listenersLock.Unlock()
		return listener, nil
	}

	// If not then create a notify channel if doesn't exist already.
	listenerNotify, found := listenersNotify[address]
	if !found {
		listenerNotify = make(chan struct{})
		listenersNotify[address] = listenerNotify
	}
	listenersLock.Unlock()

	// Wait for the notify channel to be closed (indicating a new listener has been connected), and return it.
	select {
	case <-listenerNotify:
		listenersLock.Lock()
		defer listenersLock.Unlock()
		return listeners[address], nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// EventsUpdateListeners refreshes the cluster event listener connections.
func EventsUpdateListeners(endpoints *endpoints.Endpoints, cluster *db.Cluster, serverCert func() *shared.CertInfo, members map[int64]APIHeartbeatMember, f func(int64, api.Event)) {
	// If no heartbeat members provided, populate from global database.
	if members == nil {
		var dbMembers []db.NodeInfo
		var offlineThreshold time.Duration

		err := cluster.Transaction(func(tx *db.ClusterTx) error {
			var err error

			dbMembers, err = tx.GetNodes()
			if err != nil {
				return err
			}

			offlineThreshold, err = tx.GetNodeOfflineThreshold()
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			logger.Warn("Failed to get current cluster members", log.Ctx{"err": err})
			return
		}

		members = make(map[int64]APIHeartbeatMember, len(dbMembers))
		for _, dbMember := range dbMembers {
			members[dbMember.ID] = APIHeartbeatMember{
				ID:            dbMember.ID,
				Name:          dbMember.Name,
				Address:       dbMember.Address,
				LastHeartbeat: dbMember.Heartbeat,
				Online:        !dbMember.IsOffline(offlineThreshold),
			}
		}
	}

	networkAddress := endpoints.NetworkAddress()

	keepListeners := make(map[string]struct{})
	wg := sync.WaitGroup{}
	for _, member := range members {
		// Don't bother trying to connect to ourselves or offline members.
		if member.Address == networkAddress || !member.Online {
			continue
		}

		listenersLock.Lock()
		listener, ok := listeners[member.Address]

		// If the member already has a listener associated to it, check that the listener is still active.
		// If it is, just move on to next member, but if not then we'll try to connect again.
		if ok {
			if listeners[member.Address].IsActive() {
				keepListeners[member.Address] = struct{}{} // Add to current listeners list.
				listenersLock.Unlock()
				continue
			}

			// Disconnect and delete listener, but don't delete any listenersNotify entry as there
			// might be something waiting for a future connection.
			listener.Disconnect()
			delete(listeners, member.Address)
			logger.Info("Removed inactive member event listener", log.Ctx{"local": networkAddress, "remote": member.Address})
		}
		listenersLock.Unlock()

		keepListeners[member.Address] = struct{}{} // Add to current listeners list.

		// Connect to remote concurrently and add to active listeners if successful.
		wg.Add(1)
		go func(m APIHeartbeatMember) {
			defer wg.Done()
			listener, err := eventsConnect(m.Address, endpoints.NetworkCert(), serverCert())
			if err != nil {
				logger.Warn("Failed adding member event listener", log.Ctx{"local": networkAddress, "remote": m.Address, "err": err})
				return
			}

			listener.AddHandler(nil, func(event api.Event) { f(m.ID, event) })

			listenersLock.Lock()
			listeners[m.Address] = listener

			// If there is something waiting to be notify, then notify them and delete the notifier.
			listenerNotify, found := listenersNotify[m.Address]
			if found {
				close(listenerNotify)
				delete(listenersNotify, m.Address)
			}

			logger.Info("Added member event listener", log.Ctx{"local": networkAddress, "remote": m.Address})
			listenersLock.Unlock()
		}(member)
	}

	wg.Wait()

	// Disconnect and delete any out of date listeners and their notifiers.
	listenersLock.Lock()
	for address, listener := range listeners {
		if _, found := keepListeners[address]; !found {
			listener.Disconnect()
			delete(listeners, address)
			delete(listenersNotify, address)
			logger.Info("Removed old member event listener", log.Ctx{"local": networkAddress, "remote": address})
		}
	}
	listenersLock.Unlock()
}

// Establish a client connection to get events from the given node.
func eventsConnect(address string, networkCert *shared.CertInfo, serverCert *shared.CertInfo) (*lxd.EventListener, error) {
	client, err := Connect(address, networkCert, serverCert, nil, true)
	if err != nil {
		return nil, err
	}

	return client.GetEventsAllProjects()
}
