package main

import (
	"crypto/tls"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
)

// A variation of the standard tls.Listener that supports atomically swapping
// the underlying TLS configuration. Requests served before the swap will
// continue using the old configuration.
type networkListener struct {
	net.Listener
	mu     sync.RWMutex
	config *tls.Config
}

func networkTLSListener(inner net.Listener, config *tls.Config) *networkListener {
	listener := &networkListener{
		Listener: inner,
		config:   config,
	}

	return listener
}

// Accept waits for and returns the next incoming TLS connection then use the
// current TLS configuration to handle it.
func (l *networkListener) Accept() (net.Conn, error) {
	var c net.Conn
	var err error

	// Accept() is non-blocking in go < 1.12 hence the loop and error check.
	for {
		c, err = l.Listener.Accept()
		if err == nil {
			break
		}

		if err.(net.Error).Temporary() {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		return nil, err
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	return tls.Server(c, l.config), nil
}

func ServerTLSConfig() (*tls.Config, error) {
	certInfo, err := shared.KeyPairAndCA(filepath.Join("/", "media", "lxd_config"), "agent", shared.CertServer)
	if err != nil {
		return nil, err
	}

	tlsConfig := util.ServerTLSConfig(certInfo)
	tlsConfig.CipherSuites = []uint16{
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	}

	return tlsConfig, nil
}
