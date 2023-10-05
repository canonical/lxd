//go:build !windows

package shared

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func GetOwnerMode(fInfo os.FileInfo) (os.FileMode, int, int) {
	mode := fInfo.Mode()
	uid := int(fInfo.Sys().(*syscall.Stat_t).Uid)
	gid := int(fInfo.Sys().(*syscall.Stat_t).Gid)
	return mode, uid, gid
}

func PathIsWritable(path string) bool {
	return unix.Access(path, unix.W_OK) == nil
}
