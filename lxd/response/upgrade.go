package response

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/tcp"
)

// Upgrade takes a hijacked HTTP connection and sends the HTTP 101 Switching Protocols headers for protocolName.
func Upgrade(hijackedConn net.Conn, protocolName string) error {
	return upgrade(hijackedConn, protocolName, nil)
}

// upgrade takes a hijacked HTTP connection and sends the HTTP 101 Switching Protocols headers for protocolName,
// followed by headers.
func upgrade(hijackedConn net.Conn, protocolName string, headers http.Header) error {
	// Write the status line and upgrade header by hand since w.WriteHeader() would fail after Hijack().
	sb := strings.Builder{}
	sb.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	fmt.Fprintf(&sb, "Upgrade: %s\r\n", protocolName)
	sb.WriteString("Connection: Upgrade\r\n")

	for key, values := range headers {
		for _, value := range values {
			fmt.Fprintf(&sb, "%s: %s\r\n", key, value)
		}
	}

	sb.WriteString("\r\n")

	_ = hijackedConn.SetWriteDeadline(time.Now().Add(time.Second * 5))
	n, err := hijackedConn.Write([]byte(sb.String()))
	_ = hijackedConn.SetWriteDeadline(time.Time{}) // Cancel deadline.

	if err != nil {
		return fmt.Errorf("Failed writing upgrade headers: %w", err)
	}

	if n != sb.Len() {
		return errors.New("Failed writing upgrade headers")
	}

	return nil
}

// UpgradeRelay relays the client connection of a request, once upgraded to another protocol, with a local
// connection. The response returned by Response upgrades the client connection and hands it to Run, which copies
// data between the two until either side closes or its context is done.
type UpgradeRelay struct {
	conn     net.Conn
	protocol string
	cleanup  func()

	// clientConn delivers the upgraded client connection from the response to Run. The response closes it without
	// sending when the upgrade fails.
	clientConn chan net.Conn

	// done is closed once Run has returned.
	done chan struct{}
}

// NewUpgradeRelay returns a relay between conn and a client connection upgraded to protocol. cleanup, when not nil,
// runs once the relay has ended.
func NewUpgradeRelay(conn net.Conn, protocol string, cleanup func()) *UpgradeRelay {
	return &UpgradeRelay{
		conn:       conn,
		protocol:   protocol,
		cleanup:    cleanup,
		clientConn: make(chan net.Conn),
		done:       make(chan struct{}),
	}
}

// Run waits for the upgraded client connection, relays both directions until either side closes or ctx is done and
// then closes both connections. As the run hook of the operation representing the session, it returns the ctx error
// when cancelling the operation ended the relay.
func (r *UpgradeRelay) Run(ctx context.Context) error {
	defer close(r.done)

	if r.cleanup != nil {
		defer r.cleanup()
	}

	defer func() { _ = r.conn.Close() }()

	var remoteConn net.Conn
	var ok bool
	select {
	case remoteConn, ok = <-r.clientConn:
		if !ok {
			return errors.New("Failed upgrading client connection")
		}

	case <-ctx.Done():
		return ctx.Err()
	}

	l := logger.AddContext(logger.Ctx{
		"protocol": r.protocol,
		"local":    remoteConn.LocalAddr(),
		"remote":   remoteConn.RemoteAddr(),
	})

	// Each direction cancels the relay once its copy ends, as does cancelling ctx. Both connections are closed as
	// soon as that happens, so that the other direction never stays blocked in a write to a peer that has gone
	// away, and the copy errors those closes cause are not warned about.
	relayCtx, cancel := context.WithCancel(ctx)

	wg := sync.WaitGroup{}
	wg.Go(func() {
		<-relayCtx.Done()
		_ = remoteConn.Close()
		_ = r.conn.Close()
	})

	wg.Go(func() {
		_, err := io.Copy(remoteConn, r.conn)
		if err != nil && relayCtx.Err() == nil {
			l.Warn("Failed copying local connection to remote connection", logger.Ctx{"err": err})
		}

		cancel()
	})

	_, err := io.Copy(r.conn, remoteConn)
	if err != nil && relayCtx.Err() == nil {
		l.Warn("Failed copying remote connection to local connection", logger.Ctx{"err": err})
	}

	cancel()
	wg.Wait()

	return ctx.Err()
}

// abort tells Run that no client connection is coming.
func (r *UpgradeRelay) abort() {
	close(r.clientConn)
}

// Response returns the response that upgrades the client connection and hands it to Run. op is the operation whose
// run hook is Run, and the 101 Switching Protocols response names it in a Location header so that the client keeps
// a handle on the session. The response ends once Run has returned.
func (r *UpgradeRelay) Response(op Operation) Response {
	return &upgradeResponse{relay: r, op: op}
}

// upgradeResponse switches the client connection to another protocol and relays it to a local connection.
type upgradeResponse struct {
	relay *UpgradeRelay

	// op is the operation running the relay, or nil when the response runs it itself.
	op Operation
}

// UpgradeResponse returns a response that upgrades the client connection to protocol and copies data between
// the client and conn until either side closes. cleanup, when not nil, runs once the relay has ended.
func UpgradeResponse(conn net.Conn, protocol string, cleanup func()) Response {
	return &upgradeResponse{relay: NewUpgradeRelay(conn, protocol, cleanup)}
}

// String returns the response type name.
func (r *upgradeResponse) String() string {
	return r.relay.protocol + " upgrade"
}

// Render hijacks the client connection, sends the upgrade headers, hands the connection to the relay and blocks
// until the relay has ended.
func (r *upgradeResponse) Render(w http.ResponseWriter, req *http.Request) error {
	var headers http.Header
	if r.op != nil {
		url, _ := r.op.Render()
		headers = http.Header{"Location": {url}}
	} else {
		// Without an operation to run the relay, it runs for the lifetime of the request.
		go func() { _ = r.relay.Run(req.Context()) }()
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		r.relay.abort()
		return api.StatusErrorf(http.StatusInternalServerError, "Webserver does not support hijacking")
	}

	remoteConn, _, err := hijacker.Hijack()
	if err != nil {
		r.relay.abort()
		return api.StatusErrorf(http.StatusInternalServerError, "Failed hijacking connection: %w", err)
	}

	// The hijacked connection can no longer carry an HTTP error response, so failures from here on are logged.
	l := logger.AddContext(logger.Ctx{
		"protocol": r.relay.protocol,
		"local":    remoteConn.LocalAddr(),
		"remote":   remoteConn.RemoteAddr(),
	})

	remoteTCP, err := tcp.ExtractConn(remoteConn)
	if err == nil && remoteTCP != nil {
		// Apply TCP timeouts if remote connection is TCP (rather than Unix).
		err = tcp.SetTimeouts(remoteTCP, 0)
		if err != nil {
			l.Warn("Failed setting TCP timeouts on remote connection", logger.Ctx{"err": err})
			_ = remoteConn.Close()
			r.relay.abort()
			return nil
		}
	}

	err = upgrade(remoteConn, r.relay.protocol, headers)
	if err != nil {
		l.Warn("Failed upgrading connection", logger.Ctx{"err": err})
		_ = remoteConn.Close()
		r.relay.abort()
		return nil
	}

	if r.relay.protocol == "nbd" {
		// NBD is a server-speaks-first protocol, so the server greeting leaves as soon as the relay starts.
		// A client that has not yet finished reading the HTTP 101 response discards it, which breaks the
		// handshake, so give the client a moment to complete the upgrade first.
		time.Sleep(250 * time.Millisecond)
	}

	select {
	case r.relay.clientConn <- remoteConn:
	case <-r.relay.done:
		// The relay ended before the connection could be delivered, which happens when the operation running
		// it was cancelled during the upgrade.
		_ = remoteConn.Close()
		return nil
	}

	<-r.relay.done

	if r.op == nil {
		// An operation reports the outcome of its request itself once it completes.
		metrics.UseMetricsCallback(req, metrics.Success)
	}

	return nil
}
