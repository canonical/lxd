package ucred

import (
	"fmt"
	"net"
	"reflect"
)

// UCred represents the credentials being used to access a unix socket.
type UCred struct {
	PID int32
	UID int64
	GID int64
}

// GetCred returns the credentials being used to access a unix socket.
func GetCred(conn *net.UnixConn) (*UCred, error) {
	fd, err := extractUnderlyingFd(conn)
	if err != nil {
		return nil, err
	}

	uid, gid, pid, err := GetUCred(fd)
	if err != nil {
		return nil, err
	}

	return &UCred{PID: pid, UID: int64(uid), GID: int64(gid)}, nil
}

/*
 * I also don't see that golang exports an API to get at the underlying FD, but
 * we need it to get at SO_PEERCRED, so let's grab it.
 */
func extractUnderlyingFd(unixConnPtr *net.UnixConn) (int, error) {
	conn := reflect.Indirect(reflect.ValueOf(unixConnPtr))

	netFdPtr := conn.FieldByName("fd")
	if !netFdPtr.IsValid() {
		return -1, fmt.Errorf("Unable to extract fd from net.UnixConn")
	}
	netFd := reflect.Indirect(netFdPtr)

	fd := netFd.FieldByName("sysfd")
	if !fd.IsValid() {
		// Try under the new name
		pfdPtr := netFd.FieldByName("pfd")
		if !pfdPtr.IsValid() {
			return -1, fmt.Errorf("Unable to extract pfd from netFD")
		}
		pfd := reflect.Indirect(pfdPtr)

		fd = pfd.FieldByName("Sysfd")
		if !fd.IsValid() {
			return -1, fmt.Errorf("Unable to extract Sysfd from poll.FD")
		}
	}

	return int(fd.Int()), nil
}
