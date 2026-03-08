//go:build linux && cgo

package endpoints

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
)

// CheckAlreadyRunning checks if the socket at the given path is already
// bound to a running LXD process, and return an error if so.
//
//	FIXME: We should probably rather just try a regular unix socket
//		connection without using the client. However this is the way
//		this logic has historically behaved, so let's keep it like it
//		was.
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
		return errors.New("LXD is already running")
	}

	return nil
}

// Change the ownership of the given unix socket file.
func socketUnixSetOwnership(path string, groupName string) error {
	var gid int
	var err error

	if groupName != "" {
		g, err := user.LookupGroup(groupName)
		if err != nil {
			return fmt.Errorf("cannot get group ID of %q: %w", groupName, err)
		}

		gid, err = strconv.Atoi(g.Gid)
		if err != nil {
			return err
		}
	} else {
		gid = os.Getgid()
	}

	err = os.Chown(path, os.Getuid(), gid)
	if err != nil {
		return fmt.Errorf("cannot change ownership on local socket: %w", err)
	}

	return nil
}
