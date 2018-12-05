package endpoints

import (
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	tomb "gopkg.in/tomb.v2"
)

// Config holds various configuration values that affect LXD endpoints
// initialization.
type Config struct {
	// The LXD var directory to create Unix sockets in.
	Dir string

	// UnixSocket is the path to the Unix socket to bind
	UnixSocket string

	// HTTP server handling requests for the LXD RESTful API.
	RestServer *http.Server

	// HTTP server for the internal /dev/lxd API exposed to containers.
	DevLxdServer *http.Server

	// The TLS keypair and optional CA to use for the network endpoint. It
	// must be always provided, since the pubblic key will be included in
	// the response of the /1.0 REST API as part of the server info.
	//
	// It can be updated after the endpoints are up using NetworkUpdateCert().
	Cert *shared.CertInfo

	// System group name to which the unix socket for the local endpoint should be
	// chgrp'ed when starting. The default is to use the process group. An empty
	// string means "use the default".
	LocalUnixSocketGroup string

	// NetworkSetAddress sets the address for the network endpoint. If not
	// set, the network endpoint won't be started (unless it's passed via
	// socket-based activation).
	//
	// It can be updated after the endpoints are up using UpdateNetworkAddress().
	NetworkAddress string

	// Optional dedicated network address for clustering traffic. If not
	// set, NetworkAddress will be used.
	ClusterAddress string

	// DebugSetAddress sets the address for the pprof endpoint.
	//
	// It can be updated after the endpoints are up using UpdateDebugAddress().
	DebugAddress string
}

// Up brings up all applicable LXD endpoints and starts accepting HTTP
// requests.
//
// The endpoints will be activated in the following order and according to the
// following rules:
//
// local endpoint (unix socket)
// ----------------------------
//
// If socket-based activation is detected, look for a unix socket among the
// inherited file descriptors and use it for the local endpoint (or if no such
// file descriptor exists, don't bring up the local endpoint at all).
//
// If no socket-based activation is detected, create a unix socket using the
// default <lxd-var-dir>/unix.socket path. The file mode of this socket will be set
// to 660, the file owner will be set to the process' UID, and the file group
// will be set to the process GID, or to the GID of the system group name
// specified via config.LocalUnixSocketGroup.
//
// devlxd endpoint (unix socket)
// ----------------------------
//
// Created using <lxd-var-dir>/devlxd/sock, with file mode set to 666 (actual
// authorization will be performed by the HTTP server using the socket ucred
// struct).
//
// remote endpoint (TCP socket with TLS)
// -------------------------------------
//
// If socket-based activation is detected, look for a network socket among the
// inherited file descriptors and use it for the network endpoint.
//
// If a network address was set via config.NetworkAddress, then close any listener
// that was detected via socket-based activation and create a new network
// socket bound to the given address.
//
// The network endpoint socket will use TLS encryption, using the certificate
// keypair and CA passed via config.Cert.
//
// cluster endpoint (TCP socket with TLS)
// -------------------------------------
//
// If a network address was set via config.ClusterAddress, then attach
// config.RestServer to it.
func Up(config *Config) (*Endpoints, error) {
	if config.Dir == "" {
		return nil, fmt.Errorf("No directory configured")
	}
	if config.UnixSocket == "" {
		return nil, fmt.Errorf("No unix socket configured")
	}
	if config.RestServer == nil {
		return nil, fmt.Errorf("No REST server configured")
	}
	if config.DevLxdServer == nil {
		return nil, fmt.Errorf("No devlxd server configured")
	}
	if config.Cert == nil {
		return nil, fmt.Errorf("No TLS certificate configured")
	}

	endpoints := &Endpoints{
		systemdListenFDsStart: util.SystemdListenFDsStart,
	}

	err := endpoints.up(config)
	if err != nil {
		endpoints.Down()
		return nil, err
	}
	return endpoints, nil
}

// Endpoints are in charge of bringing up and down the HTTP endpoints for
// serving the LXD RESTful API.
//
// When LXD starts up, they start listen to the appropriate sockets and attach
// the relevant HTTP handlers to them. When LXD shuts down they close all
// sockets.
type Endpoints struct {
	tomb      *tomb.Tomb            // Controls the HTTP servers shutdown.
	mu        sync.RWMutex          // Serialize access to internal state.
	listeners map[kind]net.Listener // Activer listeners by endpoint type.
	servers   map[kind]*http.Server // HTTP servers by endpoint type.
	cert      *shared.CertInfo      // Keypair and CA to use for TLS.
	inherited map[kind]bool         // Store whether the listener came through socket activation

	systemdListenFDsStart int // First socket activation FD, for tests.
}

// Up brings up all configured endpoints and starts accepting HTTP requests.
func (e *Endpoints) up(config *Config) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.servers = map[kind]*http.Server{
		devlxd:  config.DevLxdServer,
		local:   config.RestServer,
		network: config.RestServer,
		cluster: config.RestServer,
		pprof:   pprofCreateServer(),
	}
	e.cert = config.Cert
	e.inherited = map[kind]bool{}

	var err error

	// Check for socket activation.
	systemdListeners := util.GetListeners(e.systemdListenFDsStart)
	if len(systemdListeners) > 0 {
		e.listeners = activatedListeners(systemdListeners, e.cert)
		for kind := range e.listeners {
			e.inherited[kind] = true
		}
	} else {
		e.listeners = map[kind]net.Listener{}

		e.listeners[local], err = localCreateListener(config.UnixSocket, config.LocalUnixSocketGroup)
		if err != nil {
			return fmt.Errorf("local endpoint: %v", err)
		}
	}

	// Start the devlxd listener
	e.listeners[devlxd], err = createDevLxdlListener(config.Dir)
	if err != nil {
		return err
	}

	if config.NetworkAddress != "" {
		listener, ok := e.listeners[network]
		if ok {
			logger.Infof("Replacing inherited TCP socket with configured one")
			listener.Close()
			e.inherited[network] = false
		}

		// Errors here are not fatal and are just logged.
		e.listeners[network] = networkCreateListener(config.NetworkAddress, e.cert)

		isCovered := util.IsAddressCovered(config.ClusterAddress, config.NetworkAddress)
		if config.ClusterAddress != "" && !isCovered {
			e.listeners[cluster], err = clusterCreateListener(config.ClusterAddress, e.cert)
			if err != nil {
				return err
			}

			logger.Infof("Starting cluster handler:")
			e.serveHTTP(cluster)
		}

	}

	if config.DebugAddress != "" {
		e.listeners[pprof], err = pprofCreateListener(config.DebugAddress)
		if err != nil {
			return err
		}

		logger.Infof("Starting pprof handler:")
		e.serveHTTP(pprof)
	}

	logger.Infof("Starting /dev/lxd handler:")
	e.serveHTTP(devlxd)

	logger.Infof("REST API daemon:")
	e.serveHTTP(local)
	e.serveHTTP(network)

	return nil
}

// Down brings down all endpoints and stops serving HTTP requests.
func (e *Endpoints) Down() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.listeners[network] != nil || e.listeners[local] != nil {
		logger.Infof("Stopping REST API handler:")
		err := e.closeListener(network)
		if err != nil {
			return err
		}

		err = e.closeListener(local)
		if err != nil {
			return err
		}
	}

	if e.listeners[cluster] != nil {
		logger.Infof("Stopping cluster handler:")
		err := e.closeListener(cluster)
		if err != nil {
			return err
		}
	}

	if e.listeners[devlxd] != nil {
		logger.Infof("Stopping /dev/lxd handler:")
		err := e.closeListener(devlxd)
		if err != nil {
			return err
		}
	}

	if e.listeners[pprof] != nil {
		logger.Infof("Stopping pprof handler:")
		err := e.closeListener(pprof)
		if err != nil {
			return err
		}
	}

	if e.tomb != nil {
		e.tomb.Kill(nil)
		e.tomb.Wait()
	}

	return nil
}

// Start an HTTP server for the endpoint associated with the given code.
func (e *Endpoints) serveHTTP(kind kind) {
	listener := e.listeners[kind]

	if listener == nil {
		return
	}

	ctx := log.Ctx{"socket": listener.Addr()}
	if e.inherited[kind] {
		ctx["inherited"] = true
	}

	message := fmt.Sprintf(" - binding %s", descriptions[kind])
	logger.Info(message, ctx)

	server := e.servers[kind]

	// Defer the creation of the tomb, so Down() doesn't wait on it unless
	// we actually have spawned at least a server.
	if e.tomb == nil {
		e.tomb = &tomb.Tomb{}
	}

	e.tomb.Go(func() error {
		server.Serve(listener)
		return nil
	})
}

// Stop the HTTP server of the endpoint associated with the given code. The
// associated socket will be shutdown too.
func (e *Endpoints) closeListener(kind kind) error {
	listener := e.listeners[kind]
	if listener == nil {
		return nil
	}
	delete(e.listeners, kind)

	logger.Info(" - closing socket", log.Ctx{"socket": listener.Addr()})

	return listener.Close()
}

// Use the listeners associated with the file descriptors passed via
// socket-based activation.
func activatedListeners(systemdListeners []net.Listener, cert *shared.CertInfo) map[kind]net.Listener {
	listeners := map[kind]net.Listener{}
	for _, listener := range systemdListeners {
		var kind kind
		switch listener.(type) {
		case *net.UnixListener:
			kind = local
		case *net.TCPListener:
			kind = network
			listener = networkTLSListener(listener, cert)
		default:
			continue
		}
		listeners[kind] = listener
	}
	return listeners
}

// Numeric code identifying a specific API endpoint type.
type kind int

// Numeric codes identifying the various endpoints.
const (
	local kind = iota
	devlxd
	network
	pprof
	cluster
)

// Human-readable descriptions of the various kinds of endpoints.
var descriptions = map[kind]string{
	local:   "Unix socket",
	devlxd:  "devlxd socket",
	network: "TCP socket",
	pprof:   "pprof socket",
	cluster: "cluster socket",
}
