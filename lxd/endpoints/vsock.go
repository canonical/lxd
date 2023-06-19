package endpoints

import (
	"crypto/tls"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"

	"github.com/mdlayher/vsock"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
)

func createVsockListener(cert *shared.CertInfo) (net.Listener, error) {
	for i := 0; i < 10; i++ {
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

		return tls.NewListener(listener, util.ServerTLSConfig(cert)), nil
	}

	return nil, fmt.Errorf("Failed finding free listen port for vsock listener")
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
