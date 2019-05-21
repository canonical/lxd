package client

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/CanonicalLtd/go-dqlite/internal/bindings"
	"github.com/CanonicalLtd/go-dqlite/internal/logging"
	"github.com/Rican7/retry"
	"github.com/pkg/errors"
)

// Connector is in charge of creating a dqlite SQL client connected to the
// current leader of a cluster.
type Connector struct {
	id       uint64       // Client ID to use when registering against the server.
	store    ServerStore  // Used to get and update current cluster servers.
	config   Config       // Connection parameters.
	log      logging.Func // Logging function.
	protocol []byte       // Protocol version
}

// NewConnector returns a new connector that can be used by a dqlite driver to
// create new clients connected to a leader dqlite server.
func NewConnector(id uint64, store ServerStore, config Config, log logging.Func) *Connector {
	connector := &Connector{
		id:       id,
		store:    store,
		config:   config,
		log:      log,
		protocol: make([]byte, 8),
	}

	// Latest protocol version.
	binary.LittleEndian.PutUint64(
		connector.protocol,
		bindings.ProtocolVersion,
	)

	return connector
}

// Connect finds the leader server and returns a connection to it.
//
// If the connector is stopped before a leader is found, nil is returned.
func (c *Connector) Connect(ctx context.Context) (*Client, error) {
	var client *Client

	// The retry strategy should be configured to retry indefinitely, until
	// the given context is done.
	err := retry.Retry(func(attempt uint) error {
		log := func(l logging.Level, format string, a ...interface{}) {
			format += fmt.Sprintf(" attempt=%d", attempt)
			c.log(l, fmt.Sprintf(format, a...))
		}

		select {
		case <-ctx.Done():
			// Stop retrying
			return nil
		default:
		}

		var err error
		client, err = c.connectAttemptAll(ctx, log)
		if err != nil {
			log(logging.Debug, "connection failed err=%v", err)
			return err
		}

		return nil
	}, c.config.RetryStrategies...)

	if err != nil {
		// The retry strategy should never give up until success or
		// context expiration.
		panic("connect retry aborted unexpectedly")
	}

	if ctx.Err() != nil {
		return nil, ErrNoAvailableLeader
	}

	return client, nil
}

// Make a single attempt to establish a connection to the leader server trying
// all addresses available in the store.
func (c *Connector) connectAttemptAll(ctx context.Context, log logging.Func) (*Client, error) {
	servers, err := c.store.Get(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get cluster servers")
	}

	// Make an attempt for each address until we find the leader.
	for _, server := range servers {
		log := func(l logging.Level, format string, a ...interface{}) {
			format += fmt.Sprintf(" address=%s", server.Address)
			log(l, fmt.Sprintf(format, a...))
		}

		ctx, cancel := context.WithTimeout(ctx, c.config.AttemptTimeout)
		defer cancel()

		conn, leader, err := c.connectAttemptOne(ctx, server.Address)
		if err != nil {
			// This server is unavailable, try with the next target.
			log(logging.Debug, "server connection failed err=%v", err)
			continue
		}
		if conn != nil {
			// We found the leader
			log(logging.Info, "connected")
			return conn, nil
		}
		if leader == "" {
			// This server does not know who the current leader is,
			// try with the next target.
			continue
		}

		// If we get here, it means this server reported that another
		// server is the leader, let's close the connection to this
		// server and try with the suggested one.
		//logger = logger.With(zap.String("leader", leader))
		conn, leader, err = c.connectAttemptOne(ctx, leader)
		if err != nil {
			// The leader reported by the previous server is
			// unavailable, try with the next target.
			//logger.Info("leader server connection failed", zap.String("err", err.Error()))
			continue
		}
		if conn == nil {
			// The leader reported by the target server does not consider itself
			// the leader, try with the next target.
			//logger.Info("reported leader server is not the leader")
			continue
		}
		log(logging.Info, "connected")
		return conn, nil
	}

	return nil, ErrNoAvailableLeader
}

// Connect to the given dqlite server and check if it's the leader.
//
// Return values:
//
// - Any failure is hit:                     -> nil, "", err
// - Target not leader and no leader known:  -> nil, "", nil
// - Target not leader and leader known:     -> nil, leader, nil
// - Target is the leader:                   -> server, "", nil
//
func (c *Connector) connectAttemptOne(ctx context.Context, address string) (*Client, string, error) {
	// Establish the connection.
	conn, err := c.config.Dial(ctx, address)
	if err != nil {
		return nil, "", errors.Wrap(err, "failed to establish network connection")
	}

	// Perform the protocol handshake.
	n, err := conn.Write(c.protocol)
	if err != nil {
		conn.Close()
		return nil, "", errors.Wrap(err, "failed to send handshake")
	}
	if n != 8 {
		conn.Close()
		return nil, "", errors.Wrap(io.ErrShortWrite, "failed to send handshake")
	}

	client := newClient(conn, address, c.store, c.log)

	// Send the initial Leader request.
	request := Message{}
	request.Init(16)
	response := Message{}
	response.Init(512)

	EncodeLeader(&request)

	if err := client.Call(ctx, &request, &response); err != nil {
		client.Close()
		return nil, "", errors.Wrap(err, "failed to send Leader request")
	}

	leader, err := DecodeServer(&response)
	if err != nil {
		client.Close()
		return nil, "", errors.Wrap(err, "failed to parse Server response")
	}

	switch leader {
	case "":
		// Currently this server does not know about any leader.
		client.Close()
		return nil, "", nil
	case address:
		// This server is the leader, register ourselves and return.
		request.Reset()
		response.Reset()

		EncodeClient(&request, c.id)

		if err := client.Call(ctx, &request, &response); err != nil {
			client.Close()
			return nil, "", errors.Wrap(err, "failed to send Client request")
		}

		heartbeatTimeout, err := DecodeWelcome(&response)
		if err != nil {
			client.Close()
			return nil, "", errors.Wrap(err, "failed to parse Welcome response")
		}

		client.heartbeatTimeout = time.Duration(heartbeatTimeout) * time.Millisecond

		// TODO: enable heartbeat
		//go client.heartbeat()

		return client, "", nil
	default:
		// This server claims to know who the current leader is.
		client.Close()
		return nil, leader, nil
	}
}
