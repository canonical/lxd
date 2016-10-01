// +build windows

package shared

import (
	"os"
)

func GetOwner(fInfo os.FileInfo) (os.FileMode, int, int) {
	return os.FileMode(0), -1, -1
}
