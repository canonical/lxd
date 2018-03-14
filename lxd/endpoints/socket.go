package endpoints

import (
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

// Bind to the given unix socket path.
func socketUnixListen(path string) (net.Listener, error) {
	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve socket address: %v", err)
	}

	listener, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot bind socket: %v", err)
	}

	return listener, err

}

// CheckAlreadyRunning checks if the socket at the given path is already
// bound to a running LXD process, and return an error if so.
//
// FIXME: We should probably rather just try a regular unix socket
//        connection without using the client. However this is the way
//        this logic has historically behaved, so let's keep it like it
//        was.
func CheckAlreadyRunning(path string) error {
	// If socket activated, nothing to do
	pid, err := strconv.Atoi(os.Getenv("LISTEN_PID"))
	if err == nil {
		if pid == os.Getpid() {
			return nil
		}
	}

	// If there's no socket file at all, there's nothing to do.
	if !shared.PathExists(path) {
		return nil
	}

	_, err = lxd.ConnectLXDUnix(path, nil)

	// If the connection succeeded it means there's another LXD running.
	if err == nil {
		return fmt.Errorf("LXD is already running")
	}

	return nil
}

// Remove any stale socket file at the given path.
func socketUnixRemoveStale(path string) error {
	// If there's no socket file at all, there's nothing to do.
	if !shared.PathExists(path) {
		return nil
	}

	logger.Debugf("Detected stale unix socket, deleting")
	err := os.Remove(path)
	if err != nil {
		return fmt.Errorf("could not delete stale local socket: %v", err)
	}

	return nil
}

// Change the file mode of the given unix socket file,
func socketUnixSetPermissions(path string, mode os.FileMode) error {
	err := os.Chmod(path, mode)
	if err != nil {
		return fmt.Errorf("cannot set permissions on local socket: %v", err)
	}
	return nil
}

// Change the ownership of the given unix socket file,
func socketUnixSetOwnership(path string, group string) error {
	var gid int
	var err error

	if group != "" {
		gid, err = shared.GroupId(group)
		if err != nil {
			return fmt.Errorf("cannot get group ID of '%s': %v", group, err)
		}
	} else {
		gid = os.Getgid()
	}

	err = os.Chown(path, os.Getuid(), gid)
	if err != nil {
		return fmt.Errorf("cannot change ownership on local socket: %v", err)

	}

	return nil
}
