// +build !windows

package main

import (
	"os"
	"syscall"
)

func (c *fileCmd) getOwner(f *os.File) (os.FileMode, int, int, error) {
	fInfo, err := f.Stat()
	if err != nil {
		return os.FileMode(0), -1, -1, err
	}

	mode := fInfo.Mode()
	uid := int(fInfo.Sys().(*syscall.Stat_t).Uid)
	gid := int(fInfo.Sys().(*syscall.Stat_t).Gid)

	return mode, uid, gid, nil
}
