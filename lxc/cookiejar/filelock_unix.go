//go:build !windows

package cookiejar

import (
	"errors"
	"os"
	"slices"
	"syscall"
)

// flock calls the flock syscall: https://man7.org/linux/man-pages/man2/flock.2.html
// It does not call flock again if an EINTR is returned. This allows the caller to cancel the call via Ctrl+C.
func flock(f *os.File, op int) (err error) {
	// Don't allow non-blocking or invalid operations.
	if !slices.Contains([]int{syscall.LOCK_EX, syscall.LOCK_SH, syscall.LOCK_UN}, op) {
		return errors.New("Operation not supported")
	}

	fd := int(f.Fd())
	return syscall.Flock(fd, op)
}

// unlockFile calls flock with [syscall.LOCK_UN].
func unlockFile(f *os.File) error {
	return flock(f, syscall.LOCK_UN)
}

// lockFile calls flock with [syscall.LOCK_EX].
func lockFile(f *os.File) error {
	return flock(f, syscall.LOCK_EX)
}

// rlockFile calls flock with [syscall.LOCK_SH].
func rLockFile(f *os.File) error {
	return flock(f, syscall.LOCK_SH)
}
