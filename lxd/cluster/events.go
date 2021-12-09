package cluster

import (
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
var listenersLock sync.Mutex

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
			logger.Info("Added member event listener", log.Ctx{"local": networkAddress, "remote": m.Address})
			listenersLock.Unlock()
		}(member)
	}

	wg.Wait()

	// Disconnect and delete any out of date listeners.
	listenersLock.Lock()
	for address, listener := range listeners {
		if _, found := keepListeners[address]; !found {
			listener.Disconnect()
			delete(listeners, address)
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

	// Set the project to the special wildcard in order to get notified
	// about all events across all projects.
	client = client.UseProject("*")

	return client.GetEvents()
}
