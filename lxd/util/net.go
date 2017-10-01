package util

import (
	"fmt"
	"net"

	"github.com/lxc/lxd/shared"
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
