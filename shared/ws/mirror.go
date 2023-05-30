package ws

import (
	"context"
	"io"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared/logger"
)

// Mirror takes a websocket and replicates all read/write to a ReadWriteCloser.
// Returns channels indicating when reads and writes are finished (respectively).
func Mirror(ctx context.Context, conn *websocket.Conn, rwc io.ReadWriteCloser) (chan struct{}, chan struct{}) {
	return MirrorWithHooks(ctx, conn, rwc, nil, nil)
}

// MirrorWithHooks is identical to Mirror but allows for code to be run at the end of the read or write operations.
// Returns channels indicating when reads and writes are finished (respectively).
func MirrorWithHooks(ctx context.Context, conn *websocket.Conn, rwc io.ReadWriteCloser, hookRead func(conn *websocket.Conn), hookWrite func(conn *websocket.Conn)) (chan struct{}, chan struct{}) {
	logger.Debug("Websocket: Started mirror", logger.Ctx{"address": conn.RemoteAddr().String()})

	chRead := make(chan struct{}, 1)
	chWrite := make(chan struct{}, 1)
	chDone := make(chan struct{}, 1)

	connRWC := NewWrapper(conn)

	go func() {
		_, _ = io.Copy(rwc, connRWC)
		defer close(chWrite)

		// Call the hook.
		if hookRead != nil {
			hookRead(conn)
		}
	}()

	go func() {
		_, _ = io.Copy(connRWC, rwc)
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
		_ = conn.WriteMessage(websocket.CloseMessage, closeMsg)

		logger.Debug("Websocket: Stopped mirror", logger.Ctx{"address": conn.RemoteAddr().String()})
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
	chDone := make(chan struct{}, 1)
	if rc == nil {
		close(chDone)
		return chDone
	}

	logger.Debug("Websocket: Started read mirror", logger.Ctx{"address": conn.RemoteAddr().String()})

	connRWC := NewWrapper(conn)

	go func() {
		_, _ = io.Copy(connRWC, rc)
		defer close(chDone)

		// Send write barrier.
		connRWC.Close()

		logger.Debug("Websocket: Stopped read mirror", logger.Ctx{"address": conn.RemoteAddr().String()})
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
	chDone := make(chan struct{}, 1)
	if wc == nil {
		close(chDone)
		return chDone
	}

	logger.Debug("Websocket: Started write mirror", logger.Ctx{"address": conn.RemoteAddr().String()})

	connRWC := NewWrapper(conn)

	go func() {
		_, _ = io.Copy(wc, connRWC)
		defer close(chDone)

		// Send close message.
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		_ = conn.WriteMessage(websocket.CloseMessage, closeMsg)

		logger.Debug("Websocket: Stopped write mirror", logger.Ctx{"address": conn.RemoteAddr().String()})
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
