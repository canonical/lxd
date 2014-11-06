/* This is a FLEXible file which can be used by both client and daemon.
 * Teehee.
 */
package lxd

import (
	"os"
	"path/filepath"
)

/*
 * Please increment the version number every time you change the API.
 *
 * Version 0.0.1: ping
 */
var Version = "0.0.1"

// VarPath returns the provided path elements joined by a slash and
// appended to the end of $LXD_DIR, which defaults to /var/lib/lxd.
func VarPath(path ...string) string {
	varDir := os.Getenv("LXD_DIR")
	if varDir == "" {
		varDir = "/var/lib/lxd"
	}
	items := []string{varDir}
	items = append(items, path...)
	return filepath.Join(items...)
}
