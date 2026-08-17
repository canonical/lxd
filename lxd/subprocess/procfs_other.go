//go:build !linux && !windows

package subprocess

import (
	"errors"
)

func currentBootID() (string, error) {
	return "", errors.New("Procfs is only supported on Linux")
}

func processStartTime(pid int) (int64, error) {
	return 0, errors.New("Procfs is only supported on Linux")
}
