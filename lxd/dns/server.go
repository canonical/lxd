package dns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/miekg/dns"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/revert"
)

// ZoneRetriever is a function which fetches a DNS zone.
type ZoneRetriever func(name string, full bool) (*Zone, error)

// Server represents a DNS server instance.
type Server struct {
	tcpDNS *dns.Server
	udpDNS *dns.Server

	// External dependencies.
	db            *db.Cluster
	zoneRetriever ZoneRetriever

	// Internal state (to handle reconfiguration).
	address string

	mu sync.Mutex
}

// NewServer returns a new server instance.
func NewServer(db *db.Cluster, retriever ZoneRetriever) *Server {
	// Setup new struct.
	s := &Server{db: db, zoneRetriever: retriever}
	return s
}

// Start sets up the DNS listener.
func (s *Server) Start(address string) error {
	// Locking.
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.start(address)
}

func (s *Server) start(address string) error {
	// An empty address would be taken as the default port on every interface by
	// the canonicalisation below. No caller wants the listener on all
	// interfaces, and one passes the previous address when restoring a failed
	// reconfiguration, which is empty when the listener was not running.
	if address == "" {
		return errors.New("Cannot start the DNS listener without an address")
	}

	// Set default port if needed.
	address = util.CanonicalNetworkAddress(address, 53)

	// Setup the handler.
	handler := &dnsHandler{}
	handler.server = s

	// Bind both listeners before serving, so that an address that cannot be used
	// is reported to the caller instead of only logged, and so that a failure
	// cannot leave one of the two bound with nothing serving it.
	revert := revert.New()
	defer revert.Fail()

	tcpListener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("Failed binding TCP DNS address %q: %w", address, err)
	}

	revert.Add(func() { _ = tcpListener.Close() })

	udpConn, err := net.ListenPacket("udp", address)
	if err != nil {
		return fmt.Errorf("Failed binding UDP DNS address %q: %w", address, err)
	}

	revert.Add(func() { _ = udpConn.Close() })

	tcpServer := &dns.Server{Addr: address, Net: "tcp", Handler: handler, Listener: tcpListener}
	udpServer := &dns.Server{Addr: address, Net: "udp", Handler: handler, PacketConn: udpConn}

	s.tcpDNS = tcpServer
	s.udpDNS = udpServer

	revert.Add(func() {
		s.tcpDNS = nil
		s.udpDNS = nil
	})

	// TSIG handling, applied before serving so the secrets are in place for the
	// first query.
	err = s.updateTSIG()
	if err != nil {
		return err
	}

	// Serve on the bound listeners, taken from the local variables so that a
	// later change to the fields cannot affect what is being served.
	go func() {
		err := tcpServer.ActivateAndServe()
		if err != nil {
			logger.Errorf("Failed serving TCP DNS address %q: %v", address, err)
		}
	}()

	go func() {
		err := udpServer.ActivateAndServe()
		if err != nil {
			logger.Errorf("Failed serving UDP DNS address %q: %v", address, err)
		}
	}()

	// Record the address.
	s.address = address

	revert.Success()
	return nil
}

func (s *Server) stop() error {
	// Skip if no instance.
	if s.tcpDNS == nil || s.udpDNS == nil {
		return nil
	}

	// Stop the listener.
	_ = s.tcpDNS.Shutdown()
	_ = s.udpDNS.Shutdown()

	// Close the bound sockets as well: Shutdown only closes them once the server
	// has started serving, which a listener stopped before its serve goroutine
	// ran has not, and leaving it bound would keep the address in use.
	if s.tcpDNS.Listener != nil {
		_ = s.tcpDNS.Listener.Close()
	}

	if s.udpDNS.PacketConn != nil {
		_ = s.udpDNS.PacketConn.Close()
	}

	// Unset the address.
	s.address = ""
	return nil
}

// Reconfigure updates the listener with a new configuration.
func (s *Server) Reconfigure(address string) error {
	// Locking.
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.reconfigure(address)
}

func (s *Server) reconfigure(address string) error {
	// Get the old address.
	oldAddress := s.address

	// Setup reverter.
	revert := revert.New()
	defer revert.Fail()

	// Stop the listener.
	err := s.stop()
	if err != nil {
		return err
	}

	// Check if we should start.
	if address != "" {
		// Restore the previous listener on failure, if there was one: the
		// listener is stopped before the new address is tried, and there is
		// nothing to restore when it was not running.
		if oldAddress != "" {
			revert.Add(func() { _ = s.start(oldAddress) })
		}

		// Start the listener with the new address.
		err = s.start(address)
		if err != nil {
			return err
		}
	}

	// All done.
	revert.Success()
	return nil
}

// UpdateTSIG fetches all TSIG keys and loads them into the DNS server.
func (s *Server) UpdateTSIG() error {
	// Locking.
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.updateTSIG()
}

func (s *Server) updateTSIG() error {
	// Skip if no instance.
	if s.tcpDNS == nil || s.udpDNS == nil || s.db == nil {
		return nil
	}

	var secrets map[string]string

	err := s.db.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get all the secrets.
		secrets, err = tx.GetNetworkZoneKeys(ctx)

		return err
	})
	if err != nil {
		return err
	}

	// Apply to the DNS servers.
	s.tcpDNS.TsigSecret = secrets
	s.udpDNS.TsigSecret = secrets

	return nil
}
