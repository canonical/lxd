package util

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

// InMemoryNetwork creates a fully in-memory listener and dial function.
//
// Each time the dial function is invoked a new pair of net.Conn objects will
// be created using net.Pipe: the listener's Accept method will unblock and
// return one end of the pipe and the other end will be returned by the dial
// function.
func InMemoryNetwork() (net.Listener, func() net.Conn) {
	listener := &inMemoryListener{
		conns:  make(chan net.Conn, 16),
		closed: make(chan struct{}),
	}
	dialer := func() net.Conn {
		server, client := net.Pipe()
		listener.conns <- server
		return client
	}
	return listener, dialer
}

type inMemoryListener struct {
	conns  chan net.Conn
	closed chan struct{}
}

// Accept waits for and returns the next connection to the listener.
func (l *inMemoryListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, fmt.Errorf("closed")
	}
}

// Close closes the listener.
// Any blocked Accept operations will be unblocked and return errors.
func (l *inMemoryListener) Close() error {
	close(l.closed)
	return nil
}

// Addr returns the listener's network address.
func (l *inMemoryListener) Addr() net.Addr {
	return &inMemoryAddr{}
}

type inMemoryAddr struct {
}

func (a *inMemoryAddr) Network() string {
	return "memory"
}

func (a *inMemoryAddr) String() string {
	return ""
}

// CanonicalNetworkAddress parses the given network address and returns a
// string of the form "host:port", possibly filling it with the default port if
// it's missing.
func CanonicalNetworkAddress(address string) string {
	_, _, err := net.SplitHostPort(address)
	if err != nil {
		ip := net.ParseIP(address)
		if ip != nil && ip.To4() == nil {
			address = fmt.Sprintf("[%s]:%s", address, shared.DefaultPort)
		} else {
			address = fmt.Sprintf("%s:%s", address, shared.DefaultPort)
		}
	}
	return address
}

// ServerTLSConfig returns a new server-side tls.Config generated from the give
// certificate info.
func ServerTLSConfig(cert *shared.CertInfo) *tls.Config {
	config := shared.InitTLSConfig()
	config.ClientAuth = tls.RequestClientCert
	config.Certificates = []tls.Certificate{cert.KeyPair()}
	config.NextProtos = []string{"h2"} // Required by gRPC

	if cert.CA() != nil {
		pool := x509.NewCertPool()
		pool.AddCert(cert.CA())
		config.RootCAs = pool
		config.ClientCAs = pool

		logger.Infof("LXD is in CA mode, only CA-signed certificates will be allowed")
	}

	config.BuildNameToCertificate()
	return config
}

// NetworkInterfaceAddress returns the first non-loopback address of any of the
// system network interfaces.
//
// Return the empty string if none is found.
func NetworkInterfaceAddress() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if shared.IsLoopback(&iface) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		if len(addrs) == 0 {
			continue
		}
		addr, ok := addrs[0].(*net.IPNet)
		if !ok {
			continue
		}
		return addr.IP.String()
	}
	return ""
}

// IsAddressCovered detects if network address1 is actually covered by
// address2, in the sense that they are either the same address or address2 is
// specified using a wildcard with the same port of address1.
func IsAddressCovered(address1, address2 string) bool {
	if address1 == address2 {
		return true
	}

	host1, port1, err := net.SplitHostPort(address1)
	if err != nil {
		return false
	}

	host2, port2, err := net.SplitHostPort(address2)
	if err != nil {
		return false
	}

	// If the ports are different, then address1 is clearly not covered by
	// address2.
	if port2 != port1 {
		return false
	}

	// If address2 is using an IPv4 wildcard for the host, then address2 is
	// only covered if it's an IPv4 address.
	if host2 == "0.0.0.0" {
		ip := net.ParseIP(host1)
		if ip != nil && ip.To4() != nil {
			return true
		}
		return false
	}

	// If address2 is using an IPv6 wildcard for the host, then address2 is
	// always covered.
	if host2 == "::" || host2 == "" {
		return true
	}

	return false
}
