package dqlite

import (
	"fmt"
	"io/ioutil"
	"net"
	"path/filepath"
	"runtime"
	"time"

	"github.com/CanonicalLtd/go-dqlite/internal/bindings"
	"github.com/CanonicalLtd/go-dqlite/internal/replication"
	"github.com/hashicorp/raft"
	"github.com/pkg/errors"
)

// Server implements the dqlite network protocol.
type Server struct {
	log         LogFunc          // Logger
	registry    *Registry        // Registry wrapper
	server      *bindings.Server // Low-level C implementation
	listener    net.Listener     // Queue of new connections
	runCh       chan error       // Receives the low-level C server return code
	acceptCh    chan error       // Receives connection handling errors
	replication *bindings.WalReplication
	logger      *bindings.Logger
	cluster     *bindings.Cluster
}

// ServerOption can be used to tweak server parameters.
type ServerOption func(*serverOptions)

// WithServerLogFunc sets a custom log function for the server.
func WithServerLogFunc(log LogFunc) ServerOption {
	return func(options *serverOptions) {
		options.Log = log
	}
}

// WithServerAddressProvider sets a custom resolver for server addresses.
func WithServerAddressProvider(provider raft.ServerAddressProvider) ServerOption {
	return func(options *serverOptions) {
		options.AddressProvider = provider
	}
}

// NewServer creates a new Server instance.
func NewServer(raft *raft.Raft, registry *Registry, listener net.Listener, options ...ServerOption) (*Server, error) {
	o := defaultServerOptions()

	for _, option := range options {
		option(o)
	}

	replication, err := newWalReplication(registry, raft)
	if err != nil {
		return nil, err
	}

	cluster, err := newCluster(registry, raft, o.AddressProvider)
	if err != nil {
		return nil, err
	}

	server, err := bindings.NewServer(cluster)
	if err != nil {
		return nil, err
	}

	logger := bindings.NewLogger(o.Log)

	server.SetLogger(logger)
	server.SetVfs(registry.name)
	server.SetWalReplication(registry.name)

	s := &Server{
		log:         o.Log,
		registry:    registry,
		server:      server,
		listener:    listener,
		runCh:       make(chan error),
		acceptCh:    make(chan error, 1),
		logger:      logger,
		cluster:     cluster,
		replication: replication,
	}

	go s.run()

	if !s.server.Ready() {
		return nil, fmt.Errorf("server failed to start")
	}

	go s.acceptLoop()

	return s, nil
}

func newWalReplication(registry *Registry, raft *raft.Raft) (*bindings.WalReplication, error) {
	methods := replication.NewMethods(registry.registry, raft)

	replication, err := bindings.NewWalReplication(registry.name, methods)
	if err != nil {
		return nil, errors.Wrap(err, "failed to register WAL replication")
	}

	return replication, nil
}

func newCluster(registry *Registry, raft *raft.Raft, provider raft.ServerAddressProvider) (*bindings.Cluster, error) {
	methods := &cluster{
		raft:     raft,
		registry: registry.registry,
		provider: provider,
	}

	return bindings.NewCluster(methods)
}

// Hold configuration options for a dqlite server.
type serverOptions struct {
	Log             LogFunc
	AddressProvider raft.ServerAddressProvider
}

// Run the server.
func (s *Server) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	s.runCh <- s.server.Run()
}

func (s *Server) acceptLoop() {
	s.log(LogDebug, "accepting connections")

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.acceptCh <- nil
			return
		}

		err = s.server.Handle(conn)
		if err != nil {
			if err == bindings.ErrServerStopped {
				// Ignore failures due to the server being
				// stopped.
				err = nil
			}
			s.acceptCh <- err
			return
		}
	}
}

// Dump the files of a database to disk.
func (s *Server) Dump(name string, dir string) error {
	// Dump the database file.
	bytes, err := s.registry.vfs.ReadFile(name)
	if err != nil {
		return errors.Wrap(err, "failed to get database file content")
	}

	path := filepath.Join(dir, name)
	if err := ioutil.WriteFile(path, bytes, 0600); err != nil {
		return errors.Wrap(err, "failed to write database file")
	}

	// Dump the WAL file.
	bytes, err = s.registry.vfs.ReadFile(name + "-wal")
	if err != nil {
		return errors.Wrap(err, "failed to get WAL file content")
	}

	path = filepath.Join(dir, name+"-wal")
	if err := ioutil.WriteFile(path, bytes, 0600); err != nil {
		return errors.Wrap(err, "failed to write WAL file")
	}

	return nil
}

// Close the server, releasing all resources it created.
func (s *Server) Close() error {
	// Close the listener, which will make the listener.Accept() call in
	// acceptLoop() return an error.
	if err := s.listener.Close(); err != nil {
		return err
	}

	// Wait for the acceptLoop goroutine to exit.
	select {
	case err := <-s.acceptCh:
		if err != nil {
			return errors.Wrap(err, "accept goroutine failed")
		}
	case <-time.After(time.Second):
		return fmt.Errorf("accept goroutine did not stop within a second")
	}

	// Send a stop signal to the dqlite event loop.
	if err := s.server.Stop(); err != nil {
		return errors.Wrap(err, "server failed to stop")
	}

	// Wait for the run goroutine to exit.
	select {
	case err := <-s.runCh:
		if err != nil {
			return errors.Wrap(err, "accept goroutine failed")
		}
	case <-time.After(time.Second):
		return fmt.Errorf("server did not stop within a second")
	}

	s.server.Close()

	s.logger.Close()
	s.cluster.Close()
	s.replication.Close()
	s.registry.Close()

	return nil
}

// Create a serverOptions object with sane defaults.
func defaultServerOptions() *serverOptions {
	return &serverOptions{
		Log:             defaultLogFunc(),
		AddressProvider: nil,
	}
}
