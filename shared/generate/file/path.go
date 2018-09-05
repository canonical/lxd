package file

import (
	"log"
	"path/filepath"
	"runtime"
	"strings"
)

// Given its relative path with respect to the LXD surce tree, return the full
// path of a file.
func absPath(path string) string {
	// We expect to be called by code within the lxd package itself.
	_, filename, _, _ := runtime.Caller(1)

	elems := strings.Split(filename, string(filepath.Separator))
	for i := len(elems) - 1; i >= 0; i-- {
		if elems[i] == "lxd" {
			elems = append([]string{string(filepath.Separator)}, elems[:i]...)
			elems = append(elems, path)
			return filepath.Join(elems...)
		}
	}

	log.Fatalf("Could not found root dir of LXD tree source tree")

	return ""
}
