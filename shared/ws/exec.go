package ws

import (
	"context"
	"errors"
	"maps"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// CloseConns closes every connected websocket in conns and empties the map, so that a websocket arriving later
// is not registered. The websockets are closed after releasing lock, as closing a TLS connection can block.
func CloseConns(lock *sync.Mutex, conns map[int]*websocket.Conn) {
	lock.Lock()
	toClose := slices.Collect(maps.Values(conns))
	clear(conns)
	lock.Unlock()

	for _, conn := range toClose {
		if conn != nil {
			_ = conn.Close()
		}
	}
}

// WaitForConns blocks until connected is closed, ctx is done or timeout elapses.
func WaitForConns(ctx context.Context, connected <-chan struct{}, timeout time.Duration) error {
	select {
	case <-connected:
		return nil
	case <-time.After(timeout):
		return errors.New("Timed out waiting for websockets to connect")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// NewPipes creates n pipes and returns their read ends (ptys) and write ends (ttys).
func NewPipes(n int) (ptys []*os.File, ttys []*os.File, err error) {
	ptys = make([]*os.File, n)
	ttys = make([]*os.File, n)

	for i := range n {
		ptys[i], ttys[i], err = os.Pipe()
		if err != nil {
			CloseFiles(ptys)
			CloseFiles(ttys)

			return nil, nil, err
		}
	}

	return ptys, ttys, nil
}

// CloseFiles closes every non-nil file.
func CloseFiles(files []*os.File) {
	for _, f := range files {
		if f != nil {
			_ = f.Close()
		}
	}
}
