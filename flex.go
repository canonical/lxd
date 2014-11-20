/* This is a FLEXible file which can be used by both client and daemon.
 * Teehee.
 */
package lxd

import (
	"bufio"
	"os"
	"path/filepath"
)

var Version = "0.0.1"

/*
 * Please increment the api compat number every time you change the API.
 *
 * Version 1.0: ping
 */
var ApiCompat = 1
var ApiVersion = "1.0"

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

func ReadStdin() ([]byte, error) {
	buf := bufio.NewReader(os.Stdin)
	line, _, err := buf.ReadLine()
	if err != nil {
		return nil, err
	}
	return line, nil
}
