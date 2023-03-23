package shared

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared/logger"
)

// connectErrorPrefix used as prefix to error returned from RFC3493Dialer.
const connectErrorPrefix = "Unable to connect to"

// RFC3493Dialer connects to the specified server and returns the connection.
// If the connection cannot be established then an error with the connectErrorPrefix is returned.
func RFC3493Dialer(context context.Context, network string, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}

	addrs, err := net.LookupHost(host)
	if err != nil {
		return nil, err
	}

	var errs []error
	for _, a := range addrs {
		c, err := net.DialTimeout(network, net.JoinHostPort(a, port), 10*time.Second)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		tc, ok := c.(*net.TCPConn)
		if ok {
			_ = tc.SetKeepAlive(true)
			_ = tc.SetKeepAlivePeriod(3 * time.Second)
		}

		return c, nil
	}

	return nil, fmt.Errorf("%s: %s (%v)", connectErrorPrefix, address, errs)
}

// IsConnectionError returns true if the given error is due to the dialer not being able to connect to the target
// LXD server.
func IsConnectionError(err error) bool {
	// FIXME: unfortunately the LXD client currently does not provide a way to differentiate between errors.
	return strings.Contains(err.Error(), connectErrorPrefix)
}

// InitTLSConfig returns a tls.Config populated with default encryption
// parameters. This is used as baseline config for both client and server
// certificates used by LXD.
func InitTLSConfig() *tls.Config {
	config := &tls.Config{}

	// Restrict to TLS 1.3 unless LXD_INSECURE_TLS is set.
	if IsFalseOrEmpty(os.Getenv("LXD_INSECURE_TLS")) {
		config.MinVersion = tls.VersionTLS13
	} else {
		config.MinVersion = tls.VersionTLS12
	}

	return config
}

func finalizeTLSConfig(tlsConfig *tls.Config, tlsRemoteCert *x509.Certificate) {
	// Setup RootCA
	if tlsConfig.RootCAs == nil {
		tlsConfig.RootCAs, _ = systemCertPool()
	}

	// Trusted certificates
	if tlsRemoteCert != nil {
		if tlsConfig.RootCAs == nil {
			tlsConfig.RootCAs = x509.NewCertPool()
		}

		// Make it a valid RootCA
		tlsRemoteCert.IsCA = true
		tlsRemoteCert.KeyUsage = x509.KeyUsageCertSign

		// Setup the pool
		tlsConfig.RootCAs.AddCert(tlsRemoteCert)

		// Set the ServerName
		if tlsRemoteCert.DNSNames != nil {
			tlsConfig.ServerName = tlsRemoteCert.DNSNames[0]
		}
	}

	tlsConfig.BuildNameToCertificate()
}

func GetTLSConfig(tlsClientCertFile string, tlsClientKeyFile string, tlsClientCAFile string, tlsRemoteCert *x509.Certificate) (*tls.Config, error) {
	tlsConfig := InitTLSConfig()

	// Client authentication
	if tlsClientCertFile != "" && tlsClientKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(tlsClientCertFile, tlsClientKeyFile)
		if err != nil {
			return nil, err
		}

		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	if tlsClientCAFile != "" {
		caCertificates, err := os.ReadFile(tlsClientCAFile)
		if err != nil {
			return nil, err
		}

		caPool := x509.NewCertPool()
		caPool.AppendCertsFromPEM(caCertificates)

		tlsConfig.RootCAs = caPool
	}

	finalizeTLSConfig(tlsConfig, tlsRemoteCert)
	return tlsConfig, nil
}

func GetTLSConfigMem(tlsClientCert string, tlsClientKey string, tlsClientCA string, tlsRemoteCertPEM string, insecureSkipVerify bool) (*tls.Config, error) {
	tlsConfig := InitTLSConfig()
	tlsConfig.InsecureSkipVerify = insecureSkipVerify
	// Client authentication
	if tlsClientCert != "" && tlsClientKey != "" {
		cert, err := tls.X509KeyPair([]byte(tlsClientCert), []byte(tlsClientKey))
		if err != nil {
			return nil, err
		}

		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	var tlsRemoteCert *x509.Certificate
	if tlsRemoteCertPEM != "" {
		// Ignore any content outside of the PEM bytes we care about
		certBlock, _ := pem.Decode([]byte(tlsRemoteCertPEM))
		if certBlock == nil {
			return nil, fmt.Errorf("Invalid remote certificate")
		}

		var err error
		tlsRemoteCert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return nil, err
		}
	}

	if tlsClientCA != "" {
		caPool := x509.NewCertPool()
		caPool.AppendCertsFromPEM([]byte(tlsClientCA))

		tlsConfig.RootCAs = caPool
	}

	finalizeTLSConfig(tlsConfig, tlsRemoteCert)

	return tlsConfig, nil
}

func IsLoopback(iface *net.Interface) bool {
	return int(iface.Flags&net.FlagLoopback) > 0
}

func WebsocketSendStream(conn *websocket.Conn, r io.Reader, bufferSize int) chan bool {
	ch := make(chan bool)

	if r == nil {
		close(ch)
		return ch
	}

	go func(conn *websocket.Conn, r io.Reader) {
		in := ReaderToChannel(r, bufferSize)
		for {
			buf, ok := <-in
			if !ok {
				break
			}

			err := conn.WriteMessage(websocket.BinaryMessage, buf)
			if err != nil {
				logger.Debug("Got err writing", logger.Ctx{"err": err})
				break
			}
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte{})
		ch <- true
	}(conn, r)

	return ch
}

func WebsocketRecvStream(w io.Writer, conn *websocket.Conn) chan bool {
	ch := make(chan bool)

	go func(w io.Writer, conn *websocket.Conn) {
		for {
			mt, r, err := conn.NextReader()
			if mt == websocket.CloseMessage {
				logger.Debug("WebsocketRecvStream got close message for reader")
				break
			}

			if mt == websocket.TextMessage {
				logger.Debug("WebsocketRecvStream got message barrier")
				break
			}

			if err != nil {
				logger.Debug("WebsocketRecvStream got error getting next reader", logger.Ctx{"err": err})
				break
			}

			buf, err := io.ReadAll(r)
			if err != nil {
				logger.Debug("WebsocketRecvStream got error writing to writer", logger.Ctx{"err": err})
				break
			}

			if w == nil {
				continue
			}

			i, err := w.Write(buf)
			if i != len(buf) {
				logger.Debug("WebsocketRecvStream didn't write all of buf")
				break
			}

			if err != nil {
				logger.Debug("WebsocketRecvStream error writing buf", logger.Ctx{"err": err})
				break
			}
		}
		ch <- true
	}(w, conn)

	return ch
}

func WebsocketProxy(source *websocket.Conn, target *websocket.Conn) chan struct{} {
	// Forwarder between two websockets, closes channel upon disconnection.
	forward := func(in *websocket.Conn, out *websocket.Conn, ch chan struct{}) {
		for {
			mt, r, err := in.NextReader()
			if err != nil {
				break
			}

			w, err := out.NextWriter(mt)
			if err != nil {
				break
			}

			_, err = io.Copy(w, r)
			_ = w.Close()
			if err != nil {
				break
			}
		}

		close(ch)
	}

	// Spawn forwarders in both directions.
	chSend := make(chan struct{})
	go forward(source, target, chSend)

	chRecv := make(chan struct{})
	go forward(target, source, chRecv)

	// Close main channel and disconnect upon completion of either forwarder.
	ch := make(chan struct{})
	go func() {
		select {
		case <-chSend:
		case <-chRecv:
		}

		_ = source.Close()
		_ = target.Close()

		close(ch)
	}()

	return ch
}

func defaultReader(conn *websocket.Conn, r io.ReadCloser, readDone chan<- bool) {
	/* For now, we don't need to adjust buffer sizes in
	* WebsocketMirror, since it's used for interactive things like
	* exec.
	 */
	in := ReaderToChannel(r, -1)
	for {
		buf, ok := <-in
		if !ok {
			_ = r.Close()
			logger.Debug("Sending write barrier")
			_ = conn.WriteMessage(websocket.TextMessage, []byte{})
			readDone <- true
			return
		}

		err := conn.WriteMessage(websocket.BinaryMessage, buf)
		if err != nil {
			logger.Debug("Got err writing", logger.Ctx{"err": err})
			break
		}
	}
	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	_ = conn.WriteMessage(websocket.CloseMessage, closeMsg)
	readDone <- true
	_ = r.Close()
}

func DefaultWriter(conn *websocket.Conn, w io.WriteCloser, writeDone chan<- bool) {
	for {
		mt, r, err := conn.NextReader()
		if err != nil {
			logger.Debug("DefaultWriter got error getting next reader", logger.Ctx{"err": err})
			break
		}

		if mt == websocket.CloseMessage {
			logger.Debug("DefaultWriter got close message for reader")
			break
		}

		if mt == websocket.TextMessage {
			logger.Debug("DefaultWriter got message barrier, resetting stream")
			break
		}

		buf, err := io.ReadAll(r)
		if err != nil {
			logger.Debug("DefaultWriter got error writing to writer", logger.Ctx{"err": err})
			break
		}

		i, err := w.Write(buf)
		if i != len(buf) {
			logger.Debug("DefaultWriter didn't write all of buf")
			break
		}

		if err != nil {
			logger.Debug("DefaultWriter error writing buf", logger.Ctx{"err": err})
			break
		}
	}
	writeDone <- true
	_ = w.Close()
}

// WebsocketIO is a wrapper implementing ReadWriteCloser on top of websocket.
type WebsocketIO struct {
	Conn   *websocket.Conn
	reader io.Reader
	mur    sync.Mutex
	muw    sync.Mutex
}

func (w *WebsocketIO) Read(p []byte) (n int, err error) {
	w.mur.Lock()
	defer w.mur.Unlock()

	// Get new message if no active one.
	if w.reader == nil {
		var mt int

		mt, w.reader, err = w.Conn.NextReader()
		if err != nil {
			return 0, err
		}

		if mt == websocket.CloseMessage || mt == websocket.TextMessage {
			w.reader = nil // At the end of the message, reset reader.

			return 0, io.EOF
		}
	}

	// Perform the read itself.
	n, err = w.reader.Read(p)
	if err != nil {
		w.reader = nil // At the end of the message, reset reader.

		if err == io.EOF {
			return n, nil // Don't return EOF error at end of message.
		}

		return n, err
	}

	return n, nil
}

func (w *WebsocketIO) Write(p []byte) (int, error) {
	w.muw.Lock()
	defer w.muw.Unlock()

	err := w.Conn.WriteMessage(websocket.BinaryMessage, p)
	if err != nil {
		return -1, err
	}

	return len(p), nil
}

// Close sends a control message indicating the stream is finished, but it does not actually close the socket.
func (w *WebsocketIO) Close() error {
	w.muw.Lock()
	defer w.muw.Unlock()
	// Target expects to get a control message indicating stream is finished.
	return w.Conn.WriteMessage(websocket.TextMessage, []byte{})
}

// WebsocketMirror allows mirroring a reader to a websocket and taking the
// result and writing it to a writer. This function allows for multiple
// mirrorings and correctly negotiates stream endings. However, it means any
// websocket.Conns passed to it are live when it returns, and must be closed
// explicitly.
type WebSocketMirrorReader func(conn *websocket.Conn, r io.ReadCloser, readDone chan<- bool)
type WebSocketMirrorWriter func(conn *websocket.Conn, w io.WriteCloser, writeDone chan<- bool)

func WebsocketMirror(conn *websocket.Conn, w io.WriteCloser, r io.ReadCloser, Reader WebSocketMirrorReader, Writer WebSocketMirrorWriter) (chan bool, chan bool) {
	readDone := make(chan bool, 1)
	writeDone := make(chan bool, 1)

	ReadFunc := Reader
	if ReadFunc == nil {
		ReadFunc = defaultReader
	}

	WriteFunc := Writer
	if WriteFunc == nil {
		WriteFunc = DefaultWriter
	}

	go ReadFunc(conn, r, readDone)
	go WriteFunc(conn, w, writeDone)

	return readDone, writeDone
}

func WebsocketConsoleMirror(conn *websocket.Conn, w io.WriteCloser, r io.ReadCloser) (chan bool, chan bool) {
	readDone := make(chan bool, 1)
	writeDone := make(chan bool, 1)

	go DefaultWriter(conn, w, writeDone)

	go func(conn *websocket.Conn, r io.ReadCloser) {
		in := ReaderToChannel(r, -1)
		for {
			buf, ok := <-in
			if !ok {
				_ = r.Close()
				logger.Debugf("Sending write barrier")
				_ = conn.WriteMessage(websocket.BinaryMessage, []byte("\r"))
				_ = conn.WriteMessage(websocket.TextMessage, []byte{})
				readDone <- true
				return
			}

			err := conn.WriteMessage(websocket.BinaryMessage, buf)
			if err != nil {
				logger.Debugf("Got err writing %s", err)
				break
			}
		}

		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		_ = conn.WriteMessage(websocket.CloseMessage, closeMsg)
		readDone <- true
		_ = r.Close()
	}(conn, r)

	return readDone, writeDone
}

var WebsocketUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// AllocatePort asks the kernel for a free open port that is ready to use.
func AllocatePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return -1, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return -1, err
	}

	return l.Addr().(*net.TCPAddr).Port, l.Close()
}
