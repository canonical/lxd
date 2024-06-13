//go:build windows

package shared

import (
	"os"
)

// GetOwnerMode retrieves the file mode for the given file. User ID and group ID are always
// returnd as -1 on Windows OS.
func GetOwnerMode(fInfo os.FileInfo) (os.FileMode, int, int) {
	return fInfo.Mode(), -1, -1
}

// PathIsWritable returns true for any given path on Windows OS.
func PathIsWritable(path string) bool {
	return true
}
