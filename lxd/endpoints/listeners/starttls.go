package listeners

import (
	"bufio"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/cancel"
)

// StarttlsListener is a variation of the standard tls.Listener that supports
// atomically swapping the underlying TLS configuration. Requests served
// before the swap will continue using the old configuration.
type StarttlsListener struct {
	net.Listener
	mu     sync.RWMutex
	config *tls.Config

	accepted  chan acceptResult
	canceller cancel.Canceller
}

// acceptResult carries the outcome of accepting a single connection.
type acceptResult struct {
	conn net.Conn
	err  error
}

// NewSTARTTLSListener creates a new STARTTLS listener.
func NewSTARTTLSListener(inner net.Listener, cert *shared.CertInfo) *StarttlsListener {
	listener := &StarttlsListener{
		Listener:  inner,
		accepted:  make(chan acceptResult),
		canceller: cancel.New(),
	}

	listener.Config(cert)

	// Accept connections in the background.
	go listener.acceptLoop()

	return listener
}

// Accept waits for and returns the next incoming connection, upgrading it to TLS
// if the client began with a STARTTLS handshake. Once the listener is closed it
// returns net.ErrClosed instead of blocking.
func (l *StarttlsListener) Accept() (net.Conn, error) {
	// Prioritise cancellation so a closed listener never hands out a connection,
	// even if one raced into l.accepted just before Close.
	if l.canceller.Err() != nil {
		return nil, net.ErrClosed
	}

	select {
	case res := <-l.accepted:
		return res.conn, res.err
	case <-l.canceller.Done():
		return nil, net.ErrClosed
	}
}

// Close closes the underlying listener and unblocks in-flight handle goroutines.
func (l *StarttlsListener) Close() error {
	if l.canceller.Err() != nil {
		return nil
	}

	l.canceller.Cancel()
	return l.Listener.Close()
}

// acceptLoop accepts raw connections and handles each concurrently, stopping only
// once the listener is closed.
func (l *StarttlsListener) acceptLoop() {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			// Closed: stop. Accept reports the closure via the canceller.
			if errors.Is(err, net.ErrClosed) {
				return
			}

			// Transient error: forward it for the serve loop's backoff, then keep
			// accepting so the listener recovers. The canceller guard prevents
			// leaking this goroutine if Close races the send.
			select {
			case l.accepted <- acceptResult{err: err}:
			case <-l.canceller.Done():
				return
			}

			continue
		}

		go l.handle(conn)
	}
}

// handle classifies a single connection and delivers it to Accept. A failure
// only closes that connection; it is never forwarded as an Accept error, which
// would tear down the serve loop.
func (l *StarttlsListener) handle(rawConn net.Conn) {
	conn, err := l.classify(rawConn)
	if err != nil {
		_ = rawConn.Close()
		return
	}

	select {
	case l.accepted <- acceptResult{conn: conn}:
	case <-l.canceller.Done():
		// Closed mid-detection; drop rather than block on a stopped serve loop.
		_ = conn.Close()
	}
}

// classify peeks for the STARTTLS header and returns a TLS server connection if
// present, otherwise the buffered connection with its peeked bytes intact.
func (l *StarttlsListener) classify(rawConn net.Conn) (net.Conn, error) {
	// Bound the peek so an idle client cannot hold the connection open forever.
	// The deadline is cleared before returning, since the HTTP server manages its
	// own timeouts once it takes over the connection.
	_ = rawConn.SetReadDeadline(time.Now().Add(util.HTTPServerReadTimeout))
	defer func() { _ = rawConn.SetReadDeadline(time.Time{}) }()

	// Setup buffered connection.
	bufConn := BufferedUnixConn{bufio.NewReader(rawConn), rawConn.(*net.UnixConn)}

	// Peek to see if STARTTLS.
	header, err := bufConn.Peek(8)
	if err != nil {
		return nil, err
	}

	if string(header) == "STARTTLS" {
		discarded, err := bufConn.Discard(9)
		if err != nil {
			return nil, err
		}

		if discarded < 9 {
			return nil, errors.New("Bad STARTTLS header on connection")
		}

		l.mu.RLock()
		defer l.mu.RUnlock()

		config := l.config
		return tls.Server(bufConn, config), nil
	}

	return bufConn, nil
}

// Config safely swaps the underlying TLS configuration.
func (l *StarttlsListener) Config(cert *shared.CertInfo) {
	config := util.ServerTLSConfig(cert)

	l.mu.Lock()
	defer l.mu.Unlock()

	l.config = config
}

// BufferedUnixConn is a UnixConn wrapped in a Bufio Reader.
type BufferedUnixConn struct {
	r *bufio.Reader
	*net.UnixConn
}

// Discard allows discarding some bytes from the buffer.
func (b BufferedUnixConn) Discard(n int) (int, error) {
	return b.r.Discard(n)
}

// Peek allows reading some bytes without moving the read pointer.
func (b BufferedUnixConn) Peek(n int) ([]byte, error) {
	return b.r.Peek(n)
}

// Read allows normal reads on the buffered connection.
func (b BufferedUnixConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

// Unix returns the inner UnixConn.
func (b BufferedUnixConn) Unix() *net.UnixConn {
	return b.UnixConn
}
