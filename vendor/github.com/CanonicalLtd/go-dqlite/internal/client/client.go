package client

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"time"

	"github.com/CanonicalLtd/go-dqlite/internal/bindings"
	"github.com/CanonicalLtd/go-dqlite/internal/logging"
	"github.com/pkg/errors"
)

// Client connecting to a dqlite server and speaking the dqlite wire protocol.
type Client struct {
	log              logging.Func  // Logging function.
	address          string        // Address of the connected dqlite server.
	store            ServerStore   // Update this store upon heartbeats.
	conn             net.Conn      // Underlying network connection.
	heartbeatTimeout time.Duration // Heartbeat timeout reported at registration.
	contextTimeout   time.Duration // Default context timeout.
	closeCh          chan struct{} // Stops the heartbeat when the connection gets closed
	mu               sync.Mutex    // Serialize requests
}

func newClient(conn net.Conn, address string, store ServerStore, log logging.Func) *Client {
	//logger.With(zap.String("target", address)
	client := &Client{
		conn:           conn,
		address:        address,
		store:          store,
		log:            log,
		closeCh:        make(chan struct{}),
		contextTimeout: 5 * time.Second,
	}

	return client
}

// SetContextTimeout sets the default context timeout when no deadline is
// provided.
func (c *Client) SetContextTimeout(timeout time.Duration) {
	c.contextTimeout = timeout
}

// Call invokes a dqlite RPC, sending a request message and receiving a
// response message.
func (c *Client) Call(ctx context.Context, request, response *Message) error {
	// We need to take a lock since the dqlite server currently does not
	// support concurrent requests.
	c.mu.Lock()
	defer c.mu.Unlock()

	// Honor the ctx deadline, if present, or use a default.
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(c.contextTimeout)
	}

	c.conn.SetDeadline(deadline)

	if err := c.send(request); err != nil {
		return errors.Wrap(err, "failed to send request")
	}

	if err := c.recv(response); err != nil {
		return errors.Wrap(err, "failed to receive response")
	}

	return nil
}

// More is used when a request maps to multiple responses.
func (c *Client) More(ctx context.Context, response *Message) error {
	return c.recv(response)
}

// Interrupt sends an interrupt request and awaits for the server's empty
// response.
func (c *Client) Interrupt(ctx context.Context, request *Message, response *Message) error {
	// We need to take a lock since the dqlite server currently does not
	// support concurrent requests.
	c.mu.Lock()
	defer c.mu.Unlock()

	// Honor the ctx deadline, if present, or use a default.
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(2 * time.Second)
	}
	c.conn.SetDeadline(deadline)

	defer request.Reset()

	EncodeInterrupt(request, 0)

	if err := c.send(request); err != nil {
		return errors.Wrap(err, "failed to send interrupt request")
	}

	for {
		if err := c.recv(response); err != nil {
			response.Reset()
			return errors.Wrap(err, "failed to receive response")
		}

		mtype, _ := response.getHeader()
		response.Reset()

		if mtype == bindings.ResponseEmpty {
			break
		}
	}

	return nil
}

// Close the client connection.
func (c *Client) Close() error {
	c.log(bindings.LogInfo, "closing client")

	close(c.closeCh)

	return c.conn.Close()
}

func (c *Client) send(req *Message) error {
	if err := c.sendHeader(req); err != nil {
		return errors.Wrap(err, "failed to send header")
	}

	if err := c.sendBody(req); err != nil {
		return errors.Wrap(err, "failed to send body")
	}

	return nil
}

func (c *Client) sendHeader(req *Message) error {
	n, err := c.conn.Write(req.header[:])
	if err != nil {
		return errors.Wrap(err, "failed to send header")
	}

	if n != messageHeaderSize {
		return errors.Wrap(io.ErrShortWrite, "failed to send header")
	}

	return nil
}

func (c *Client) sendBody(req *Message) error {
	buf := req.body1.Bytes[:req.body1.Offset]
	n, err := c.conn.Write(buf)
	if err != nil {
		return errors.Wrap(err, "failed to send static body")
	}

	if n != len(buf) {
		return errors.Wrap(io.ErrShortWrite, "failed to write body")
	}

	if req.body2.Bytes == nil {
		return nil
	}

	buf = req.body2.Bytes[:req.body2.Offset]
	n, err = c.conn.Write(buf)
	if err != nil {
		return errors.Wrap(err, "failed to send dynamic body")
	}

	if n != len(buf) {
		return errors.Wrap(io.ErrShortWrite, "failed to write body")
	}

	return nil
}

func (c *Client) recv(res *Message) error {
	if err := c.recvHeader(res); err != nil {
		return errors.Wrap(err, "failed to receive header")
	}

	if err := c.recvBody(res); err != nil {
		return errors.Wrap(err, "failed to receive body")
	}

	return nil
}

func (c *Client) recvHeader(res *Message) error {
	if err := c.recvPeek(res.header); err != nil {
		return errors.Wrap(err, "failed to receive header")
	}

	res.words = binary.LittleEndian.Uint32(res.header[0:])
	res.mtype = res.header[4]
	res.flags = res.header[5]
	res.extra = binary.LittleEndian.Uint16(res.header[6:])

	return nil
}

func (c *Client) recvBody(res *Message) error {
	n := int(res.words) * messageWordSize
	n1 := n
	n2 := 0

	if n1 > len(res.body1.Bytes) {
		// We need to allocate the dynamic buffer.
		n1 = len(res.body1.Bytes)
		n2 = n - n1
	}

	buf := res.body1.Bytes[:n1]

	if err := c.recvPeek(buf); err != nil {
		return errors.Wrap(err, "failed to read body")
	}

	if n2 > 0 {
		res.body2.Bytes = make([]byte, n2)
		res.body2.Offset = 0
		buf = res.body2.Bytes
		if err := c.recvPeek(buf); err != nil {
			return errors.Wrap(err, "failed to read body")
		}
	}

	return nil
}

// Read until buf is full.
func (c *Client) recvPeek(buf []byte) error {
	for offset := 0; offset < len(buf); {
		n, err := c.recvFill(buf[offset:])
		if err != nil {
			return err
		}
		offset += n
	}

	return nil
}

// Try to fill buf, but perform at most one read.
func (c *Client) recvFill(buf []byte) (int, error) {
	// Read new data: try a limited number of times.
	//
	// This technique is copied from bufio.Reader.
	for i := messageMaxConsecutiveEmptyReads; i > 0; i-- {
		n, err := c.conn.Read(buf)
		if n < 0 {
			panic(errNegativeRead)
		}
		if err != nil {
			return -1, err
		}
		if n > 0 {
			return n, nil
		}
	}
	return -1, io.ErrNoProgress
}

func (c *Client) heartbeat() {
	request := Message{}
	request.Init(16)
	response := Message{}
	response.Init(512)

	for {
		delay := c.heartbeatTimeout / 3

		//c.logger.Debug("sending heartbeat", zap.Duration("delay", delay))
		time.Sleep(delay)

		// Check if we've been closed.
		select {
		case <-c.closeCh:
			return
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)

		EncodeHeartbeat(&request, uint64(time.Now().Unix()))

		err := c.Call(ctx, &request, &response)

		// We bail out upon failures.
		//
		// TODO: make the client survive temporary disconnections.
		if err != nil {
			cancel()
			//c.logger.Error("heartbeat failed", zap.Error(err))
			return
		}

		//addresses, err := DecodeServers(&response)
		_, err = DecodeServers(&response)
		if err != nil {
			cancel()
			//c.logger.Error("invalid heartbeat response", zap.Error(err))
			return
		}

		// if err := c.store.Set(ctx, addresses); err != nil {
		// 	cancel()
		// 	c.logger.Error("failed to update servers", zap.Error(err))
		// 	return
		// }

		cancel()

		request.Reset()
		response.Reset()
	}
}
