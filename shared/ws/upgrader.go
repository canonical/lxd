package ws

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/tcp"
)

// Upgrader is a websocket upgrader which ignores the request Origin.
var Upgrader = websocket.Upgrader{
	CheckOrigin:      func(r *http.Request) bool { return true },
	HandshakeTimeout: time.Second * 5,
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
