package endpoints

import (
	"fmt"
	"net"
	"time"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

func metricsCreateListener(address string, cert *shared.CertInfo) (net.Listener, error) {
	return createTLSListener(address, shared.HTTPSMetricsDefaultPort, cert)
}

// MetricsAddress returns the network address of the metrics endpoint, or an
// empty string if there's no metrics endpoint.
func (e *Endpoints) MetricsAddress() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	listener := e.listeners[metrics]
	if listener == nil {
		return ""
	}

	return listener.Addr().String()
}

// MetricsUpdateAddress updates the address for the metrics endpoint, shutting it down and restarting it.
func (e *Endpoints) MetricsUpdateAddress(address string, cert *shared.CertInfo) error {
	if address != "" {
		address = util.CanonicalNetworkAddress(address, shared.HTTPSMetricsDefaultPort)
	}

	oldAddress := e.MetricsAddress()
	if address == oldAddress {
		return nil
	}

	logger.Info("Update metrics address")

	e.mu.Lock()
	defer e.mu.Unlock()

	// Close the previous socket
	_ = e.closeListener(metrics)

	// If turning off listening, we're done
	if address == "" {
		return nil
	}

	// Attempt to setup the new listening socket
	getListener := func(address string) (*net.Listener, error) {
		var err error
		var listener net.Listener

		for range 10 { // Ten retries over a second seems reasonable.
			listener, err = metricsCreateListener(address, cert)
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
				e.listeners[metrics] = *listener
				e.serve(metrics)
			}

			return err
		}

		e.listeners[metrics] = *listener
		e.serve(metrics)
	}

	return nil
}
