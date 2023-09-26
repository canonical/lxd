package endpoints

import (
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof" // pprof magic
	"time"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

// PprofCreateServer creates a standalone HTTP server dedicated to serving Go's pprof debugging requests.
func pprofCreateServer() *http.Server {
	// Undo the magic that importing pprof does
	pprofMux := http.DefaultServeMux
	http.DefaultServeMux = http.NewServeMux()

	// Setup an http server
	srv := &http.Server{
		Handler: pprofMux,
	}

	return srv
}

// PprofCreateListener creates a TCP listener for the Go pprof debugging server on a specific address.
func pprofCreateListener(address string) (net.Listener, error) {
	return net.Listen("tcp", address)
}

// PprofAddress returns the network address of the pprof endpoint, or an empty string if there's no pprof endpoint.
func (e *Endpoints) PprofAddress() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	listener := e.listeners[pprof]
	if listener == nil {
		return ""
	}

	return listener.Addr().String()
}

// PprofUpdateAddress updates the address for the pprof endpoint, shutting it down and restarting it.
func (e *Endpoints) PprofUpdateAddress(address string) error {
	if address != "" {
		address = util.CanonicalNetworkAddress(address, shared.HTTPDefaultPort)
	}

	oldAddress := e.NetworkAddress()
	if address == oldAddress {
		return nil
	}

	logger.Infof("Update pprof address")

	e.mu.Lock()
	defer e.mu.Unlock()

	// Close the previous socket
	_ = e.closeListener(pprof)

	// If turning off listening, we're done
	if address == "" {
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
				e.listeners[pprof] = *listener
				e.serve(pprof)
			}

			return err
		}

		e.listeners[pprof] = *listener
		e.serve(pprof)
	}

	return nil
}
