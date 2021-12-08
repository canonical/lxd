package ucred

import (
	"fmt"
	"net"
	"net/http"
	"reflect"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ErrNotUnixSocket is returned when the underlying connection isn't a unix socket.
var ErrNotUnixSocket = fmt.Errorf("Connection isn't a unix socket")

// GetCred returns the credentials from the remote end of a unix socket.
func GetCred(conn *net.UnixConn) (*unix.Ucred, error) {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}

	var ucred *unix.Ucred
	var ucredErr error
	err = rawConn.Control(func(fd uintptr) {
		ucred, ucredErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return nil, err
	}

	if ucredErr != nil {
		return nil, ucredErr
	}

	return ucred, nil
}

// GetConnFromWriter extracts the connection from the client on a HTTP listener.
func GetConnFromWriter(w http.ResponseWriter) net.Conn {
	v := reflect.Indirect(reflect.ValueOf(w))
	connPtr := v.FieldByName("conn")
	conn := reflect.Indirect(connPtr)
	rwc := conn.FieldByName("rwc")

	netConnPtr := (*net.Conn)(unsafe.Pointer(rwc.UnsafeAddr()))
	return *netConnPtr
}

// GetCredFromWriter extracts the unix credentials from the client on a HTTP listener.
func GetCredFromWriter(w http.ResponseWriter) (*unix.Ucred, error) {
	conn := GetConnFromWriter(w)
	unixConnPtr, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, ErrNotUnixSocket
	}

	return GetCred(unixConnPtr)
}
