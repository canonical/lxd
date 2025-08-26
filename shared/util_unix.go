//go:build !windows

package shared

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
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
