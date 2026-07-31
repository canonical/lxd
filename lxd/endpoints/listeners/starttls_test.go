package listeners

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared"
)

// newTestUnixListener returns a StarttlsListener on a temporary unix socket.
func newTestUnixListener(t *testing.T) *StarttlsListener {
	t.Helper()

	file, err := os.CreateTemp("", "lxd-starttls-test")
	require.NoError(t, err)

	path := file.Name()
	require.NoError(t, file.Close())
	require.NoError(t, os.Remove(path))

	addr, err := net.ResolveUnixAddr("unix", path)
	require.NoError(t, err)

	inner, err := net.ListenUnix("unix", addr)
	require.NoError(t, err)

	listener := NewSTARTTLSListener(inner, shared.TestingKeyPair())
	t.Cleanup(func() { _ = listener.Close() })

	return listener
}

// An idle connection that sends no bytes must not block acceptance of others
// (issue #18705).
func TestStarttlsListener_IdleConnectionDoesNotBlockAccept(t *testing.T) {
	listener := newTestUnixListener(t)
	sockPath := listener.Addr().String()

	// Connect but send nothing.
	idle, err := net.Dial("unix", sockPath)
	require.NoError(t, err)
	defer func() { _ = idle.Close() }()

	// A second client that sends immediately.
	active, err := net.Dial("unix", sockPath)
	require.NoError(t, err)
	defer func() { _ = active.Close() }()

	_, err = active.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	require.NoError(t, err)

	// Accept must return the active connection despite the idle one.
	type acceptOut struct {
		conn net.Conn
		err  error
	}

	done := make(chan acceptOut, 1)
	go func() {
		conn, err := listener.Accept()
		done <- acceptOut{conn: conn, err: err}
	}()

	select {
	case out := <-done:
		require.NoError(t, out.err)
		require.NotNil(t, out.conn)

		// The accepted connection is the active one.
		header, err := bufio.NewReader(out.conn).Peek(3)
		require.NoError(t, err)
		assert.Equal(t, "GET", string(header))

	case <-time.After(5 * time.Second):
		t.Fatal("Accept blocked on the idle connection")
	}
}

// A plain connection is returned as a BufferedUnixConn so SO_PEERCRED keeps
// working.
func TestStarttlsListener_PlainConnectionPassthrough(t *testing.T) {
	listener := newTestUnixListener(t)
	sockPath := listener.Addr().String()

	client, err := net.Dial("unix", sockPath)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	_, err = client.Write([]byte("PING/1.0 hello"))
	require.NoError(t, err)

	conn, err := listener.Accept()
	require.NoError(t, err)

	bufConn, ok := conn.(BufferedUnixConn)
	require.True(t, ok, "plain connection should be a BufferedUnixConn")
	require.NotNil(t, bufConn.Unix(), "underlying UnixConn must be reachable for SO_PEERCRED")

	buf := make([]byte, 4)
	_, err = conn.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "PING", string(buf))
}

// A client that sends the STARTTLS header is upgraded to a TLS connection over
// which application data flows.
func TestStarttlsListener_STARTTLSUpgrade(t *testing.T) {
	listener := newTestUnixListener(t)
	sockPath := listener.Addr().String()

	clientErr := make(chan error, 1)
	go func() {
		clientErr <- func() error {
			raw, err := net.Dial("unix", sockPath)
			if err != nil {
				return err
			}

			defer func() { _ = raw.Close() }()

			// Send the STARTTLS header, then perform the TLS handshake.
			_, err = raw.Write([]byte("STARTTLS\n"))
			if err != nil {
				return err
			}

			client := tls.Client(raw, &tls.Config{InsecureSkipVerify: true})
			err = client.Handshake()
			if err != nil {
				return err
			}

			_, err = client.Write([]byte("ping"))
			return err
		}()
	}()

	conn, err := listener.Accept()
	require.NoError(t, err)

	server, ok := conn.(*tls.Conn)
	require.True(t, ok, "STARTTLS connection should upgrade to *tls.Conn")

	// Reading drives the server-side handshake and returns the client's data.
	buf := make([]byte, 4)
	_, err = io.ReadFull(server, buf)
	require.NoError(t, err)
	assert.Equal(t, "ping", string(buf))

	require.NoError(t, <-clientErr)
}

// Closing the listener must unblock a pending Accept with an error, so the HTTP
// server's serve loop terminates on shutdown.
func TestStarttlsListener_CloseUnblocksAccept(t *testing.T) {
	listener := newTestUnixListener(t)

	accepted := make(chan error, 1)
	go func() {
		_, err := listener.Accept()
		accepted <- err
	}()

	require.NoError(t, listener.Close())

	select {
	case err := <-accepted:
		require.Error(t, err, "Accept must return an error after Close")
	case <-time.After(5 * time.Second):
		t.Fatal("Accept did not return after Close")
	}
}
