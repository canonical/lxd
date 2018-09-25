// +build !windows

package termios

import (
	"syscall"
	"unsafe"

	"github.com/lxc/lxd/shared"
)

// #include <termios.h>
// #cgo CFLAGS: -std=gnu11 -Wvla
import "C"

// State contains the state of a terminal.
type State struct {
	Termios syscall.Termios
}

// IsTerminal returns true if the given file descriptor is a terminal.
func IsTerminal(fd int) bool {
	_, err := GetState(fd)
	return err == nil
}

// GetState returns the current state of a terminal which may be useful to restore the terminal after a signal.
func GetState(fd int) (*State, error) {
	termios := syscall.Termios{}

	ret, err := C.tcgetattr(C.int(fd), (*C.struct_termios)(unsafe.Pointer(&termios)))
	if ret != 0 {
		return nil, err.(syscall.Errno)
	}

	state := State{}
	state.Termios = termios

	return &state, nil
}

// GetSize returns the dimensions of the given terminal.
func GetSize(fd int) (int, int, error) {
	var dimensions [4]uint16

	if _, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&dimensions)), 0, 0, 0); err != 0 {
		return -1, -1, err
	}

	return int(dimensions[1]), int(dimensions[0]), nil
}

// MakeRaw put the terminal connected to the given file descriptor into raw mode and returns the previous state of the terminal so that it can be restored.
func MakeRaw(fd int) (*State, error) {
	var err error
	var oldState, newState *State

	oldState, err = GetState(fd)
	if err != nil {
		return nil, err
	}

	err = shared.DeepCopy(&oldState, &newState)
	if err != nil {
		return nil, err
	}

	C.cfmakeraw((*C.struct_termios)(unsafe.Pointer(&newState.Termios)))

	err = Restore(fd, newState)
	if err != nil {
		return nil, err
	}

	return oldState, nil
}

// Restore restores the terminal connected to the given file descriptor to a previous state.
func Restore(fd int, state *State) error {
	ret, err := C.tcsetattr(C.int(fd), C.TCSANOW, (*C.struct_termios)(unsafe.Pointer(&state.Termios)))
	if ret != 0 {
		return err.(syscall.Errno)
	}

	return nil
}
