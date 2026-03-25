package ws

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/tcp"
)

// Upgrader is a websocket upgrader that validates the Origin header.
var Upgrader = websocket.Upgrader{
	CheckOrigin:      checkOrigin,
	HandshakeTimeout: time.Second * 5,
}

// isStandardWebScheme reports whether the given URL scheme is a standard
// browser-originated scheme that carries meaningful port information.
func isStandardWebScheme(scheme string) bool {
	switch strings.ToLower(scheme) {
	case "http", "https", "ws", "wss":
		return true
	}

	return false
}

// checkOrigin implements origin validation for WebSocket upgrade requests.
//
// It allows programmatic (non-browser) clients that do not send an Origin
// header. For browser clients that do, it enforces strict same-origin
// (host:port) matching when the Origin uses a standard web scheme
// (http, https, ws, wss).
//
// For non-standard Origin schemes (e.g. "ws+unix://" sent by pylxd/ws4py),
// the comparison falls back to hostname-only matching so that legitimate
// programmatic clients are not spuriously rejected.
func checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin header (non-browser client): allow.
		return true
	}

	u, err := url.Parse(origin)
	if err != nil {
		return false
	}

	// For standard web schemes, compare the full host:port to preserve
	// strict same-origin semantics (matching gorilla's default behavior).
	requestHost, requestPort, splitErr := net.SplitHostPort(r.Host)
	if splitErr != nil {
		// r.Host has no port (e.g. plain "localhost").
		requestHost = r.Host
	} else if isStandardWebScheme(u.Scheme) {
		// Both sides carry a valid port: require full host:port match.
		return strings.EqualFold(u.Host, net.JoinHostPort(requestHost, requestPort))
	}

	// Non-standard scheme (e.g. ws+unix) or Host with a malformed/missing
	// port: fall back to hostname-only comparison.
	return strings.EqualFold(u.Hostname(), requestHost)
}

// StartKeepAlive sets TCP_USER_TIMEOUT and TCP keep alive timeouts on a connection and starts a periodic websocket
// ping go routine if the underlying connection is TCP. Otherwise this is a no-op.
func StartKeepAlive(conn *websocket.Conn) {
	// Set TCP timeout options.
	remoteTCP, err := tcp.ExtractConn(conn.NetConn())
	if err != nil || remoteTCP == nil {
		return
	}

	err = tcp.SetTimeouts(remoteTCP, 0)
	if err != nil {
		logger.Warn("Failed setting TCP timeouts on remote connection", logger.Ctx{"err": err})
	}

	// Start channel keep alive to run until channel is closed.
	go func() {
		pingInterval := time.Second * 10
		t := time.NewTicker(pingInterval)
		defer t.Stop()

		for {
			err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			if err != nil {
				return
			}

			<-t.C
		}
	}()
}
