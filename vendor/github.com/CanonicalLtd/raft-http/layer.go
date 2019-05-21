package rafthttp

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/CanonicalLtd/raft-membership"
	"github.com/hashicorp/raft"
	"github.com/pkg/errors"
)

// NewLayer returns a new raft stream layer that initiates connections
// with HTTP and then uses Upgrade to switch them into raw TCP.
func NewLayer(path string, localAddr net.Addr, handler *Handler, dial Dial) *Layer {
	//logger := log.New(os.Stderr, "", log.LstdFlags)
	logger := log.New(ioutil.Discard, "", 0)
	return NewLayerWithLogger(path, localAddr, handler, dial, logger)
}

// NewLayerWithLogger returns a Layer using the specified logger.
func NewLayerWithLogger(path string, localAddr net.Addr, handler *Handler, dial Dial, logger *log.Logger) *Layer {
	return &Layer{
		path:      path,
		localAddr: localAddr,
		handler:   handler,
		dial:      dial,
		logger:    logger,
	}
}

// Layer represents the connection between raft nodes.
type Layer struct {
	path      string
	localAddr net.Addr
	handler   *Handler
	dial      Dial
	logger    *log.Logger
}

// Accept waits for the next connection.
func (l *Layer) Accept() (net.Conn, error) {
	select {
	case conn := <-l.handler.connections:
		return conn, nil
	case <-l.handler.shutdown:
		return nil, io.EOF
	}
}

// Close closes the layer.
func (l *Layer) Close() error {
	l.handler.Close()
	return nil
}

// Addr returns the local address for the layer.
func (l *Layer) Addr() net.Addr {
	return l.localAddr
}

// Dial creates a new network connection.
func (l *Layer) Dial(addr raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	l.logger.Printf("[INFO] raft-http: Connecting to %s", addr)

	url := makeURL(l.path)
	request := &http.Request{
		Method:     "GET",
		URL:        url,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Host:       l.Addr().String(),
	}
	request.Header.Set("Upgrade", "raft")

	conn, err := l.dial(string(addr), timeout)
	if err != nil {
		return nil, errors.Wrap(err, "dialing failed")
	}

	if err := request.Write(conn); err != nil {
		return nil, errors.Wrap(err, "sending HTTP request failed")
	}

	response, err := http.ReadResponse(bufio.NewReader(conn), request)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read response")
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		return nil, fmt.Errorf("dialing fail: expected status code 101 got %d", response.StatusCode)
	}
	if response.Header.Get("Upgrade") != "raft" {
		return nil, fmt.Errorf("missing or unexpected Upgrade header in response")
	}
	return conn, err
}

// Join tries to join the cluster by contacting the leader at the given
// address. The raft node associated with this layer must have the given server
// identity.
func (l *Layer) Join(id raft.ServerID, addr raft.ServerAddress, timeout time.Duration) error {
	l.logger.Printf("[INFO] raft-http: Joining cluster at %s as node %s", addr, id)

	return l.changeMemberhip(raftmembership.JoinRequest, id, addr, timeout)
}

// Leave tries to leave the cluster by contacting the leader at the given
// address.  The raft node associated with this layer must have the given
// server identity.
func (l *Layer) Leave(id raft.ServerID, addr raft.ServerAddress, timeout time.Duration) error {
	l.logger.Printf("[INFO] raft-http: Leaving cluster at %s as node %s", addr, id)

	return l.changeMemberhip(raftmembership.LeaveRequest, id, addr, timeout)
}

// Change the membership of the server associated with this layer.
func (l *Layer) changeMemberhip(kind raftmembership.ChangeRequestKind, id raft.ServerID, addr raft.ServerAddress, timeout time.Duration) error {
	return ChangeMembership(kind, l.path, l.dial, id, l.Addr().String(), string(addr), timeout)
}

// Map a membership ChangeRequest kind code to an HTTP method name.
var membershipChangeRequestKindToMethod = map[raftmembership.ChangeRequestKind]string{
	raftmembership.JoinRequest:  "POST",
	raftmembership.LeaveRequest: "DELETE",
}
