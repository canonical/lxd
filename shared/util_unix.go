//go:build !windows

package shared

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared/logger"
)

// GetOwnerMode retrieves the file mode, user ID, and group ID for the given file.
func GetOwnerMode(fInfo os.FileInfo) (mode os.FileMode, uid int, gid int) {
	mode = fInfo.Mode()
	uid = int(fInfo.Sys().(*syscall.Stat_t).Uid)
	gid = int(fInfo.Sys().(*syscall.Stat_t).Gid)
	return mode, uid, gid
}

// PathIsWritable returns true if the given path is writable and false otherwise.
func PathIsWritable(path string) bool {
	return unix.Access(path, unix.W_OK) == nil
}

// ListenUnix binds to the given unix socket path and returns a listener.
func ListenUnix(path string) (net.Listener, error) {
	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return nil, fmt.Errorf("Failed resolving socket address %q: %w", path, err)
	}

	listener, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("Failed binding socket %q: %w", path, err)
	}

	return listener, nil
}

// RemoveUnixSocket removes any stale socket file at the given path.
func RemoveUnixSocket(path string) error {
	err := os.Remove(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("Failed deleting stale local socket %q: %w", path, err)
	}

	logger.Debug("Deleted stale unix socket")

	return nil
}

// SetUnixSocketPermissions changes the file mode of the given unix socket file.
func SetUnixSocketPermissions(path string, mode os.FileMode) error {
	err := os.Chmod(path, mode)
	if err != nil {
		return fmt.Errorf("Failed setting permissions on local socket %q: %w", path, err)
	}

	return nil
}
