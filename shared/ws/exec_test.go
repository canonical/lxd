package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloseConns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		defer func() { _ = conn.Close() }()

		_, _, _ = conn.ReadMessage()
	}))

	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)

	var lock sync.Mutex
	conns := map[int]*websocket.Conn{0: conn, 1: nil}

	CloseConns(&lock, conns)

	assert.Empty(t, conns)

	require.True(t, lock.TryLock(), "lock must be released")
	lock.Unlock()

	_, _, err = conn.ReadMessage()
	require.Error(t, err, "websocket must be closed")
}

func TestWaitForConns(t *testing.T) {
	t.Run("connected", func(t *testing.T) {
		connected := make(chan struct{})
		close(connected)

		require.NoError(t, WaitForConns(context.Background(), connected, time.Second))
	})

	t.Run("timeout", func(t *testing.T) {
		err := WaitForConns(context.Background(), make(chan struct{}), time.Millisecond)
		require.Error(t, err)
	})

	t.Run("context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := WaitForConns(ctx, make(chan struct{}), time.Second)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestNewPipes(t *testing.T) {
	ptys, ttys, err := NewPipes(3)
	require.NoError(t, err)
	require.Len(t, ptys, 3)
	require.Len(t, ttys, 3)

	_, err = ttys[1].WriteString("x")
	require.NoError(t, err)

	buf := make([]byte, 1)
	_, err = ptys[1].Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "x", string(buf))

	CloseFiles(ttys)
	CloseFiles(ptys)
}
