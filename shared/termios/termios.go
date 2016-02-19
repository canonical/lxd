// +build !windows

package termios

import (
	"syscall"
	"unsafe"

	"github.com/lxc/lxd/shared"
)

// #include <termios.h>
import "C"

type State struct {
	Termios syscall.Termios
}

func IsTerminal(fd int) bool {
	_, err := GetState(fd)
	return err == nil
}

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

func GetSize(fd int) (int, int, error) {
	var dimensions [4]uint16

	if _, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&dimensions)), 0, 0, 0); err != 0 {
		return -1, -1, err
	}

	return int(dimensions[1]), int(dimensions[0]), nil
}

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

func Restore(fd int, state *State) error {
	ret, err := C.tcsetattr(C.int(fd), C.TCSANOW, (*C.struct_termios)(unsafe.Pointer(&state.Termios)))
	if ret != 0 {
		return err.(syscall.Errno)
	}

	return nil
}
