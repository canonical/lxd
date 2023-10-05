//go:build windows

package shared

import (
	"os"
)

func GetOwnerMode(fInfo os.FileInfo) (os.FileMode, int, int) {
	return fInfo.Mode(), -1, -1
}

func PathIsWritable(path string) bool {
	return true
}
