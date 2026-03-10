package tcp

import (
	"crypto/tls"
	"errors"
	"net"
	"reflect"
	"time"
	"unsafe"
)

// ExtractConn tries to extract the underlying net.TCPConn from a tls.Conn or net.Conn.
func ExtractConn(conn net.Conn) (*net.TCPConn, error) {
	var tcpConn *net.TCPConn

	// Go doesn't currently expose the underlying TCP connection of a TLS connection, but we need it in order
	// to set timeout properties on the connection. We use some reflect/unsafe magic to extract the private
	// remote.conn field, which is indeed the underlying TCP connection.
	tlsConn, ok := conn.(*tls.Conn)
	if ok {
		field := reflect.ValueOf(tlsConn).Elem().FieldByName("conn")
		field = reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
		c := field.Interface()

		tcpConn, ok = c.(*net.TCPConn)
		if !ok {
			return nil, errors.New("Underlying tls.Conn connection is not a net.TCPConn")
		}
	} else {
		tcpConn, ok = conn.(*net.TCPConn)
		if !ok {
			return nil, errors.New("Connection is not a net.TCPConn")
		}
	}

	return tcpConn, nil
}

// KeepAliveTimeouts defines the TCP keep alive configuration to be applied to connections.
// - TCP keep alive idle time set to 3 seconds.
// - TCP keep alive interval set to 15 seconds.
// - TCP keep alive count set to 9.
// - userTimeout set to a value slightly longer than the total time it would take for all keep alive probes to be sent.
func KeepAliveTimeouts() (keepaliveConfig net.KeepAliveConfig, userTimeout time.Duration) {
	keepaliveConfig = net.KeepAliveConfig{
		Enable:   true,
		Idle:     3 * time.Second,
		Interval: 15 * time.Second,
		Count:    9,
	}

	// Set TCP_USER_TIMEOUT option to limit the maximum amount of time in ms that transmitted data may remain
	// unacknowledged before TCP will forcefully close the corresponding connection and return ETIMEDOUT to the
	// application. This combined with the TCP keepalive options on the socket will ensure that should the
	// remote side of the connection disappear abruptly that LXD will detect this and close the socket quickly.
	// Decreasing the user timeouts allows applications to "fail fast" if so desired. Otherwise it may take
	// up to 20 minutes with the current system defaults in a normal WAN environment if there are packets in
	// the send queue that will prevent the keepalive timer from working as the retransmission timers kick in.
	// See https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/commit/?id=dca43c75e7e545694a9dd6288553f55c53e2a3a3
	// userTimeout is set to a value slightly longer than the total time it would take for all keep alive
	// probes to be sent.
	userTimeout = keepaliveConfig.Idle + (keepaliveConfig.Interval * time.Duration(keepaliveConfig.Count))

	return keepaliveConfig, userTimeout
}

// SetTimeouts sets TCP_USER_TIMEOUT and TCP keep alive timeouts on a connection using values from KeepAliveTimeouts.
// If userTimeout is set to 0 then the default value from KeepAliveTimeouts will be used.
func SetTimeouts(conn *net.TCPConn, userTimeout time.Duration) error {
	kc, defaultUserTimeout := KeepAliveTimeouts()
	if userTimeout == 0 {
		userTimeout = defaultUserTimeout
	}

	err := conn.SetKeepAliveConfig(kc)
	if err != nil {
		return err
	}

	err = SetUserTimeout(conn, userTimeout)
	if err != nil {
		return err
	}

	return nil
}
