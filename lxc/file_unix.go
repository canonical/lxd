// +build !windows

package main

import (
	"os"
	"path/filepath"
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

func (c *fileCmd) normalize(path string, target string) string {
	/* Fix up the path. Let's:
	 * 1. re-add the leading / that got stripped from the SplitN
	 * 2. clean it and remove any /./, /../, /////, etc.
	 * 3. keep the trailing slash if it had one, since we use it via
	 *    filepath.Split below
	 */
	path = filepath.Clean("/" + path)
	if target[len(target)-1] == '/' {
		path = path + "/"
	}

	return path
}
