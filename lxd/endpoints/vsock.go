package endpoints

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"path/filepath"

	"github.com/mdlayher/vsock"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/endpoints/listeners"
	"github.com/canonical/lxd/lxd/ucred"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

func createVsockListener(cert *shared.CertInfo) (net.Listener, error) {
	for range 10 {
		// Get random port between 1024 and 65535.
		port := 1024 + rand.Int31n(math.MaxUint16-1024)

		// Setup listener on host context ID for inbound connections from lxd-agent running inside VMs.
		listener, err := vsock.ListenContextID(vsock.Host, uint32(port), nil)
		if err != nil {
			// Try a different port.
			if errors.Is(err, unix.EADDRINUSE) {
				continue
			}

			return nil, err
		}

		return listeners.NewFancyTLSListener(listener, cert), nil
	}

	return nil, errors.New("Failed finding free listen port for vsock listener")
}

// createVsockUnixListener creates the TLS unix socket listener that the libkrun helpers of
// MicroVMs connect to for their agents. The socket is only accessible to root.
func createVsockUnixListener(dir string, cert *shared.CertInfo) (net.Listener, error) {
	path := filepath.Join(dir, "vsock-unix.socket")

	err := shared.RemoveUnixSocket(path)
	if err != nil {
		return nil, err
	}

	listener, err := shared.ListenUnix(path)
	if err != nil {
		return nil, err
	}

	err = shared.SetUnixSocketPermissions(path, 0600)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}

	return listeners.NewFancyTLSListener(&microVMPeerListener{Listener: listener}, cert), nil
}

// microVMPeerAddr is the remote address of a connection on the MicroVM unix socket: the PID of
// the connecting libkrun helper, in the form "microvm(<pid>)".
type microVMPeerAddr int32

// Network returns the network of the address.
func (a microVMPeerAddr) Network() string {
	return "unix"
}

// String returns the address as "microvm(<pid>)".
func (a microVMPeerAddr) String() string {
	return fmt.Sprintf("microvm(%d)", int32(a))
}

// microVMPeerConn is a connection whose remote address is the PID of its peer.
type microVMPeerConn struct {
	net.Conn

	addr microVMPeerAddr
}

// RemoteAddr returns the PID of the peer as a microVMPeerAddr.
func (c *microVMPeerConn) RemoteAddr() net.Addr {
	return c.addr
}

// microVMPeerListener sets the remote address of each accepted connection to the PID of the
// connecting process, so the API server can tell which MicroVM a connection comes from.
type microVMPeerListener struct {
	net.Listener
}

// Accept returns the next connection whose peer credentials can be read. Others are closed, as
// returning an error would stop the HTTP server.
func (l *microVMPeerListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}

		unixConn, ok := conn.(*net.UnixConn)
		if !ok {
			_ = conn.Close()
			continue
		}

		cred, err := ucred.GetCred(unixConn)
		if err != nil {
			logger.Debug("Dropped MicroVM agent connection without peer credentials", logger.Ctx{"err": err})
			_ = conn.Close()
			continue
		}

		return &microVMPeerConn{Conn: conn, addr: microVMPeerAddr(cred.Pid)}, nil
	}
}

// VsockAddress returns the network address of the vsock endpoint, or nil if there's no vsock endpoint.
func (e *Endpoints) VsockAddress() net.Addr {
	e.mu.RLock()
	defer e.mu.RUnlock()

	listener := e.listeners[vmvsock]
	if listener == nil {
		return nil
	}

	return listener.Addr()
}
