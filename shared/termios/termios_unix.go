//go:build !windows
// +build !windows

package termios

import (
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared"
)

// #include <termios.h>
import "C"

// State contains the state of a terminal.
type State struct {
	Termios unix.Termios
}

// IsTerminal returns true if the given file descriptor is a terminal.
func IsTerminal(fd int) bool {
	_, err := GetState(fd)
	return err == nil
}

// GetState returns the current state of a terminal which may be useful to restore the terminal after a signal.
func GetState(fd int) (*State, error) {
	termios, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return nil, err
	}

	state := State{}
	state.Termios = *termios

	return &state, nil
}

// GetSize returns the dimensions of the given terminal.
func GetSize(fd int) (int, int, error) {
	winsize, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return -1, -1, err
	}

	return int(winsize.Col), int(winsize.Row), nil
}

func copyTermios(state *State, cTermios *C.struct_termios) {
	cTermios.c_iflag = C.tcflag_t(state.Termios.Iflag)
	cTermios.c_oflag = C.tcflag_t(state.Termios.Oflag)
	cTermios.c_cflag = C.tcflag_t(state.Termios.Cflag)
	cTermios.c_lflag = C.tcflag_t(state.Termios.Lflag)
	cTermios.c_line = C.cc_t(state.Termios.Line)
	cTermios.c_ispeed = C.speed_t(state.Termios.Ispeed)
	cTermios.c_ospeed = C.speed_t(state.Termios.Ospeed)

	for i := 0; i < len(state.Termios.Cc) && i < C.NCCS; i++ {
		cTermios.c_cc[i] = C.uchar(i)
	}
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

	var cTermios C.struct_termios
	copyTermios(newState, &cTermios)
	C.cfmakeraw(&cTermios)

	err = Restore(fd, newState)
	if err != nil {
		return nil, err
	}

	return oldState, nil
}

// Restore restores the terminal connected to the given file descriptor to a previous state.
func Restore(fd int, state *State) error {
	var cTermios C.struct_termios
	copyTermios(state, &cTermios)
	ret, err := C.tcsetattr(C.int(fd), C.TCSANOW, &cTermios)
	if ret != 0 {
		return err.(unix.Errno)
	}

	return nil
}
