package ws

import (
	"context"
	"io"

	"github.com/gorilla/websocket"
	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/shared/logger"
)

// Mirror takes a websocket and replicates all read/write to a ReadWriteCloser.
func Mirror(ctx context.Context, conn *websocket.Conn, rwc io.ReadWriteCloser) (chan struct{}, chan struct{}) {
	return MirrorWithHooks(ctx, conn, rwc, nil, nil)
}

// MirrorWithHooks is identical to Mirror but allows for code to be run at the end of the read or write operations.
func MirrorWithHooks(ctx context.Context, conn *websocket.Conn, rwc io.ReadWriteCloser, hookRead func(conn *websocket.Conn), hookWrite func(conn *websocket.Conn)) (chan struct{}, chan struct{}) {
	logger.Debug("Websocket: Started mirror", log.Ctx{"address": conn.RemoteAddr().String()})

	chRead := make(chan struct{}, 1)
	chWrite := make(chan struct{}, 1)
	chDone := make(chan struct{}, 1)

	connRWC := NewWrapper(conn)

	go func() {
		io.Copy(rwc, connRWC)
		defer close(chWrite)

		// Call the hook.
		if hookRead != nil {
			hookRead(conn)
		}
	}()

	go func() {
		io.Copy(connRWC, rwc)
		defer close(chRead)

		// Call the hook.
		if hookWrite != nil {
			hookWrite(conn)
		}

		// Send write barrier.
		connRWC.Close()
	}()

	go func() {
		<-chRead
		<-chWrite
		close(chDone)

		// Send close message.
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		conn.WriteMessage(websocket.CloseMessage, closeMsg)

		logger.Debug("Websocket: Stopped mirror", log.Ctx{"address": conn.RemoteAddr().String()})
	}()

	go func() {
		// Handle cancelation.
		select {
		case <-ctx.Done():
		case <-chDone:
		}

		// Close the ReadWriteCloser on termination.
		rwc.Close()
	}()

	return chRead, chWrite
}

// MirrorRead is a uni-directional mirror which replicates an io.ReadCloser to a websocket.
func MirrorRead(ctx context.Context, conn *websocket.Conn, rc io.ReadCloser) chan struct{} {
	logger.Debug("Websocket: Started read mirror", log.Ctx{"address": conn.RemoteAddr().String()})

	chDone := make(chan struct{}, 1)
	connRWC := NewWrapper(conn)

	go func() {
		io.Copy(connRWC, rc)
		defer close(chDone)

		// Send write barrier.
		connRWC.Close()

		logger.Debug("Websocket: Stopped read mirror", log.Ctx{"address": conn.RemoteAddr().String()})
	}()

	go func() {
		// Handle cancelation.
		select {
		case <-ctx.Done():
			// Close the ReadCloser on cancel.
			rc.Close()
		case <-chDone:
		}
	}()

	return chDone
}

// MirrorWrite is a uni-directional mirror which replicates a websocket to an io.WriteCloser.
func MirrorWrite(ctx context.Context, conn *websocket.Conn, wc io.WriteCloser) chan struct{} {
	logger.Debug("Websocket: Started write mirror", log.Ctx{"address": conn.RemoteAddr().String()})

	chDone := make(chan struct{}, 1)
	connRWC := NewWrapper(conn)

	go func() {
		io.Copy(wc, connRWC)
		defer close(chDone)

		// Send close message.
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		conn.WriteMessage(websocket.CloseMessage, closeMsg)

		logger.Debug("Websocket: Stopped write mirror", log.Ctx{"address": conn.RemoteAddr().String()})
	}()

	go func() {
		// Handle cancelation.
		select {
		case <-ctx.Done():
			// Close the WriteCloser on cancel.
			wc.Close()
		case <-chDone:
		}
	}()

	return chDone
}
