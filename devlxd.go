/*
 * In case anyone wants to write go code that talks to /dev/lxd, here's an HTTP
 * transport that correctly sends unix credentials to LXD. Example usage is
 * something like:
 *
 * c := http.Client{Transport: lxd.DevLxdTransport}
 *
 * See /lxd/devlxd_test.go for a complete example.
 */
package lxd

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"syscall"
)

type DevLxdDialer struct {
	Path string
}

func (d DevLxdDialer) DevLxdDial(network, path string) (net.Conn, error) {
	addr, err := net.ResolveUnixAddr("unix", d.Path)
	if err != nil {
		return nil, err
	}

	conn, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		return nil, err
	}

	ucred := syscall.Ucred{
		Pid: int32(os.Getpid()),
		Uid: uint32(os.Getuid()),
		Gid: uint32(os.Getgid()),
	}

	oob := syscall.UnixCredentials(&ucred)
	_, oobn, err := conn.WriteMsgUnix(nil, oob, nil)
	if err != nil {
		return nil, err
	}

	if oobn == 0 {
		return nil, fmt.Errorf("write of unix creds failed")
	}

	return conn, err
}

var DevLxdTransport = &http.Transport{
	Dial: DevLxdDialer{"/dev/lxd"}.DevLxdDial,
}
