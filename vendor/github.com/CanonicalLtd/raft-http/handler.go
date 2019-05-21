package rafthttp

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/CanonicalLtd/raft-membership"
	"github.com/hashicorp/raft"
	"github.com/pkg/errors"
)

// Handler implements an HTTP handler that will look for an Upgrade
// header in the request to switch the HTTP connection to raw TCP
// mode, so it can be used as raft.NetworkTransport stream.
type Handler struct {
	requests    chan *raftmembership.ChangeRequest // Membership requests are pushed to this channel
	connections chan net.Conn                      // New Raft connections are pushed to this channel.
	shutdown    chan struct{}                      // Used to stop processing membership requests.
	timeout     time.Duration                      // Maximum time to wait for requests to be processed.
	logger      *log.Logger                        // Logger to use.
	mu          sync.RWMutex                       // Blocks closing until all membership requests are handled
}

// NewHandler returns a new Handler.
//
// Incoming raft membership requests (received via POST and DELETE) are
// forwarded to the given channel, which is supposed to be processed using
// raftmembership.HandleChangeRequests().
func NewHandler() *Handler {
	logger := log.New(os.Stderr, "", log.LstdFlags)
	return NewHandlerWithLogger(logger)
}

// NewHandlerWithLogger returns a new Handler configured with the given logger.
func NewHandlerWithLogger(logger *log.Logger) *Handler {
	return &Handler{
		requests:    make(chan *raftmembership.ChangeRequest),
		connections: make(chan net.Conn),
		shutdown:    make(chan struct{}),
		timeout:     10 * time.Second,
		logger:      logger,
	}
}

// Requests returns a channel of inbound Raft membership change requests
// received over HTTP. Consumer code is supposed to process this channel by
// invoking raftmembership.HandleChangeRequests.
func (h *Handler) Requests() <-chan *raftmembership.ChangeRequest {
	return h.requests
}

// Timeout sets the maximum amount of time for a request to be processed. It
// defaults to 10 seconds if not set.
func (h *Handler) Timeout(timeout time.Duration) {
	h.timeout = timeout
}

// Close stops handling incoming requests.
func (h *Handler) Close() {
	close(h.shutdown)

	// Block until all pending requests are done. After that no new
	// requests will be sent to the requests channel since the shutdown
	// channel is closed.
	h.mu.Lock()
	defer h.mu.Unlock()

	close(h.requests)
}

// ServerHTTP upgrades the given HTTP connection to a raw TCP one for
// use by raft.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		h.handleGet(w, r)
	case "POST":
		h.handlePost(w, r)
	case "DELETE":
		h.handleDelete(w, r)
	default:
		http.Error(w, "unknown action", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Upgrade") != "raft" {
		http.Error(w, "missing or invalid upgrade header", http.StatusBadRequest)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "webserver doesn't support hijacking", http.StatusInternalServerError)
		return
	}

	conn, _, err := hijacker.Hijack()
	if err != nil {
		message := errors.Wrap(err, "failed to hijack connection").Error()
		http.Error(w, message, http.StatusInternalServerError)
		return
	}

	// Write the status line and upgrade header by hand since w.WriteHeader()
	// would fail after Hijack()
	data := []byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: raft\r\n\r\n")
	if n, err := conn.Write(data); err != nil || n != len(data) {
		conn.Close()
		return
	}

	// We don't need to watch for the shutdown channel here, because if the
	// shutdown channel gets closed, Layer.Accept() will eventually return
	// error causing the raft.NetworkTransport.listen() method to return
	// (the assumption is that the raft instance is being shutdown). At
	// that point, nobody will be calling Layer.Accept() anymore and we'll
	// block sending to the h.connections channel until h.timeout expires.
	h.logger.Printf("[INFO] raft-http: Establishing new connection with %s", r.Host)
	select {
	case <-h.shutdown:
		h.logger.Printf("[ERR] raft-http: Connection from %s dropped since we have shutdowns", r.Host)
		conn.Close()
	case h.connections <- conn:
	case <-time.After(h.timeout):
		h.logger.Printf("[ERR] raft-http: Connection from %s not processed within %s", r.Host, h.timeout)
		conn.Close()
	}
}

func (h *Handler) handlePost(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	id := raft.ServerID(query.Get("id"))
	address := raft.ServerAddress(query.Get("address"))

	h.logger.Printf("[INFO] raft-http: Handling join request for node %s (%s)", id, address)

	request := raftmembership.NewJoinRequest(id, address)
	h.changeMembership(w, r, request)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	id := raft.ServerID(query.Get("id"))

	h.logger.Printf("[INFO] raft-http: Handling leave request for node %s", id)

	request := raftmembership.NewLeaveRequest(id)
	h.changeMembership(w, r, request)
}

func (h *Handler) changeMembership(w http.ResponseWriter, r *http.Request, request *raftmembership.ChangeRequest) {
	// Acquire a read lock, so Close() will block until all change
	// membership requests are done.
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Fail immediately if we've been closed.
	select {
	case <-h.shutdown:
		http.Error(w, "raft transport closed", http.StatusForbidden)
		return
	default:
	}

	// Sanity check before actually trying to process the request.
	if request.ID() == "" {
		http.Error(w, "no server ID provided", http.StatusBadRequest)
		return
	}

	// It's safe to block here, since HandleChangeRequests has an internal
	// timeout, which will abort a request if takes too long.
	h.requests <- request

	err := request.Error(h.timeout)
	if err == nil {
		return
	}

	var code int

	switch err := err.(type) {
	case *raftmembership.ErrDifferentLeader:
		// If we fail because the current node is not the leader, send
		// a redirect.
		url := &url.URL{
			Scheme:   "http", // XXX TODO: handle HTTPS
			Path:     r.URL.Path,
			RawQuery: r.URL.RawQuery,
			Host:     err.Leader(),
		}
		http.Redirect(w, r, url.String(), http.StatusPermanentRedirect)
		return
	case *raftmembership.ErrUnknownLeader:
		// If we fail because we currently don't know the leader, hint
		// the client to retry.
		code = http.StatusServiceUnavailable
	default:
		code = http.StatusForbidden
	}

	message := errors.Wrap(err, fmt.Sprintf(
		"failed to %s server %s", request.Kind(), request.ID())).Error()
	http.Error(w, message, code)
}
