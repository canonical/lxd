package endpoints

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

// NetworkPublicKey returns the public key of the TLS certificate used by the
// network endpoint.
func (e *Endpoints) NetworkPublicKey() []byte {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return e.cert.PublicKey()
}

// NetworkAddress returns the network addresss of the network endpoint, or an
// empty string if there's no network endpoint
func (e *Endpoints) NetworkAddress() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	listener := e.listeners[network]
	if listener == nil {
		return ""
	}
	return listener.Addr().String()
}

// NetworkUpdateAddress updates the address for the network endpoint, shutting
// it down and restarting it.
func (e *Endpoints) NetworkUpdateAddress(address string) error {
	if address != "" {
		address = util.CanonicalNetworkAddress(address)
	}

	if address == e.NetworkAddress() {
		return nil
	}

	logger.Infof("Update network address")

	// First try to see if we can listen to this new port at all, so we
	// don't close the old one (if any) in case of errors.
	var listener net.Listener
	if address != "" {
		var err error
		for i := 0; i < 10; i++ { // Ten retries over a second seems reasonable.
			listener, err = net.Listen("tcp", address)
			if err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil {
			return fmt.Errorf("cannot listen on https socket: %v", err)
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.closeListener(network)

	if address != "" {
		e.listeners[network] = networkTLSListener(listener, e.cert)
		e.serveHTTP(network)
	}

	return nil
}

// NetworkUpdateCert updates the TLS keypair and CA used by the network
// endpoint.
//
// If the network endpoint is active, in-flight requests will continue using
// the old certificate, and only new requests will use the new one.
func (e *Endpoints) NetworkUpdateCert(cert *shared.CertInfo) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cert = cert
	listener, ok := e.listeners[network]
	if !ok {
		return
	}
	listener.(*networkListener).Config(cert)
}

// Create a new net.Listener bound to the tcp socket of the network endpoint.
func networkCreateListener(address string, cert *shared.CertInfo) net.Listener {
	listener, err := net.Listen("tcp", util.CanonicalNetworkAddress(address))
	if err != nil {
		logger.Error("cannot listen on https socket, skipping...", log.Ctx{"err": err})
		return nil
	}
	return networkTLSListener(listener, cert)
}

// A variation of the standard tls.Listener that supports atomically swapping
// the underlying TLS configuration. Requests served before the swap will
// continue using the old configuration.
type networkListener struct {
	net.Listener
	mu     sync.RWMutex
	config *tls.Config
}

func networkTLSListener(inner net.Listener, cert *shared.CertInfo) *networkListener {
	listener := &networkListener{
		Listener: inner,
	}
	listener.Config(cert)
	return listener
}

// Accept waits for and returns the next incoming TLS connection then use the
// current TLS configuration to handle it.
func (l *networkListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	config := l.config
	return tls.Server(c, config), nil
}

// Config safely swaps the underlying TLS configuration.
func (l *networkListener) Config(cert *shared.CertInfo) {
	config := shared.InitTLSConfig()
	config.ClientAuth = tls.RequestClientCert
	config.Certificates = []tls.Certificate{cert.KeyPair()}

	if cert.CA() != nil {
		pool := x509.NewCertPool()
		pool.AddCert(cert.CA())
		config.RootCAs = pool
		config.ClientCAs = pool

		logger.Infof("LXD is in CA mode, only CA-signed certificates will be allowed")
	}

	config.BuildNameToCertificate()

	l.mu.Lock()
	defer l.mu.Unlock()
	l.config = config
}
