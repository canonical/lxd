package endpoints

import (
	"fmt"
	"net"
	"time"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

func storageBucketsCreateListener(address string, cert *shared.CertInfo) (net.Listener, error) {
	return createTLSListener(address, shared.HTTPSStorageBucketsDefaultPort, cert)
}

// StorageBucketsAddress returns the network address of the storage buckets endpoint, or an
// empty string if there's no storage buckets endpoint.
func (e *Endpoints) StorageBucketsAddress() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	listener := e.listeners[storageBuckets]
	if listener == nil {
		return ""
	}

	return listener.Addr().String()
}

// StorageBucketsUpdateAddress updates the address for the storage buckets endpoint, shutting it down and
// restarting it.
func (e *Endpoints) StorageBucketsUpdateAddress(address string, cert *shared.CertInfo) error {
	if address != "" {
		address = util.CanonicalNetworkAddress(address, shared.HTTPSStorageBucketsDefaultPort)
	}

	oldAddress := e.StorageBucketsAddress()
	if address == oldAddress {
		return nil
	}

	logger.Info("Update storage buckets address")

	e.mu.Lock()
	defer e.mu.Unlock()

	// Close the previous socket
	_ = e.closeListener(storageBuckets)

	// If turning off listening, we're done
	if address == "" {
		return nil
	}

	// Attempt to setup the new listening socket
	getListener := func(address string) (*net.Listener, error) {
		var err error
		var listener net.Listener

		for range 10 { // Ten retries over a second seems reasonable.
			listener, err = storageBucketsCreateListener(address, cert)
			if err == nil {
				break
			}

			time.Sleep(100 * time.Millisecond)
		}

		if err != nil {
			return nil, fmt.Errorf("Cannot listen on http socket: %w", err)
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
				e.listeners[storageBuckets] = *listener
				e.serve(storageBuckets)
			}

			return err
		}

		e.listeners[storageBuckets] = *listener
		e.serve(storageBuckets)
	}

	return nil
}
