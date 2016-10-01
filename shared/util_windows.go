// +build windows

package shared

import (
	"os"
)

func GetOwnerMode(fInfo os.FileInfo) (os.FileMode, int, int) {
	return os.FileMode(0), -1, -1
}
