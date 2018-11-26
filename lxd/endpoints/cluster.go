package endpoints

import (
	"fmt"
	"net"
	"time"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
	"github.com/pkg/errors"
)

// ClusterAddress returns the cluster addresss of the cluster endpoint, or an
// empty string if there's no cluster endpoint.
func (e *Endpoints) ClusterAddress() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	listener := e.listeners[cluster]
	if listener == nil {
		return ""
	}
	return listener.Addr().String()
}

// ClusterUpdateAddress updates the address for the cluster endpoint, shutting
// it down and restarting it.
func (e *Endpoints) ClusterUpdateAddress(address string) error {
	networkAddress := e.NetworkAddress()

	if address != "" {
		address = util.CanonicalNetworkAddress(address)
	}

	oldAddress := e.ClusterAddress()
	if address == oldAddress {
		return nil
	}

	logger.Infof("Update cluster address")

	e.mu.Lock()
	defer e.mu.Unlock()

	// Close the previous socket
	e.closeListener(cluster)

	// If turning off listening, we're done
	if address == "" || util.IsAddressCovered(address, networkAddress) {
		return nil
	}

	// Attempt to setup the new listening socket
	getListener := func(address string) (*net.Listener, error) {
		var err error
		var listener net.Listener

		for i := 0; i < 10; i++ { // Ten retries over a second seems reasonable.
			listener, err = net.Listen("tcp", address)
			if err == nil {
				break
			}

			time.Sleep(100 * time.Millisecond)
		}

		if err != nil {
			return nil, fmt.Errorf("cannot listen on https socket: %v", err)
		}

		return &listener, nil
	}

	// If setting a new address, setup the listener
	if address != "" {
		listener, err := getListener(address)
		if err != nil {
			// Attempt to revert to the previous address
			listener, err1 := getListener(oldAddress)
			if err1 == nil {
				e.listeners[cluster] = networkTLSListener(*listener, e.cert)
				e.serveHTTP(cluster)
			}

			return err
		}

		e.listeners[cluster] = networkTLSListener(*listener, e.cert)
		e.serveHTTP(cluster)
	}

	return nil
}

func clusterCreateListener(address string, cert *shared.CertInfo) (net.Listener, error) {
	listener, err := net.Listen("tcp", util.CanonicalNetworkAddress(address))
	if err != nil {
		return nil, errors.Wrap(err, "Listen to cluster address")
	}

	return networkTLSListener(listener, cert), nil
}
