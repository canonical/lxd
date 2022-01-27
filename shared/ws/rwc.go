package ws

import (
	"io"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared/logger"
)

// NewWrapper returns a new ReadWriteCloser wrapper for a websocket connection.
func NewWrapper(conn *websocket.Conn) io.ReadWriteCloser {
	return &Wrapper{conn: conn}
}

// Wrapper is a wrapper implementing ReadWriteCloser on top of a websocket connection.
type Wrapper struct {
	conn   *websocket.Conn
	reader io.Reader
	mu     sync.Mutex
	closed bool
}

func (w *Wrapper) Read(p []byte) (n int, err error) {
	// Processing new message.
	if w.reader == nil {
		mt, wr, err := w.conn.NextReader()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				// Websocket was cleanly disconnected.
				logger.Debug("Websocket: Got disconnected", logger.Ctx{"address": w.conn.RemoteAddr().String()})
				return 0, io.EOF
			}

			// Failed to get a reader.
			logger.Debug("Websocket: Failed to get reader", logger.Ctx{"err": err, "address": w.conn.RemoteAddr().String()})
			return -1, err
		}

		w.mu.Lock()
		w.reader = wr
		w.mu.Unlock()

		if mt == websocket.CloseMessage {
			// Websocket closed by remote.
			logger.Debug("Websocket: Got close message", logger.Ctx{"address": w.conn.RemoteAddr().String()})
			return 0, io.EOF
		}

		if mt == websocket.TextMessage {
			// Barrier message, done with this stream.
			logger.Debug("Websocket: Got barrier message", logger.Ctx{"address": w.conn.RemoteAddr().String()})
			return 0, io.EOF
		}
	}

	// Perform the read itself.
	n, err = w.reader.Read(p)
	if err == io.EOF {
		// At the end of the message, reset for next one.
		w.mu.Lock()
		w.reader = nil
		w.mu.Unlock()

		return n, nil
	}

	if err != nil {
		// Failed to read the message.
		logger.Debug("Websocket: Failed to read message", logger.Ctx{"err": err, "address": w.conn.RemoteAddr().String()})
		return -1, err
	}

	return n, nil
}

func (w *Wrapper) Write(p []byte) (n int, err error) {
	// Locking.
	w.mu.Lock()
	defer w.mu.Unlock()

	// Send the data as a binary message.
	wr, err := w.conn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return -1, err
	}

	defer wr.Close()

	n, err = wr.Write(p)
	if err != nil {
		return -1, err
	}

	return n, nil
}

// Close sends a control message indicating the stream is finished, but it does not actually close the socket.
func (w *Wrapper) Close() error {
	// Locking.
	w.mu.Lock()
	defer w.mu.Unlock()

	// Don't send the barrier multiple times.
	if w.closed {
		return io.ErrClosedPipe
	}

	// Mark as closed.
	w.closed = true

	// Send the barrier message (don't actually close the socket so we can use it for another stream).
	logger.Debug("Websocket: Sending barrier message", logger.Ctx{"address": w.conn.RemoteAddr().String()})
	return w.conn.WriteMessage(websocket.TextMessage, []byte{})
}
