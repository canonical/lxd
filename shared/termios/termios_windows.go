// +build windows

package termios

import (
	"golang.org/x/crypto/ssh/terminal"
)

type State terminal.State

func IsTerminal(fd int) bool {
	return terminal.IsTerminal(fd)
}

func GetState(fd int) (*State, error) {
	state, err := terminal.GetState(fd)
	if err != nil {
		return nil, err
	}

	currentState := State(*state)
	return &currentState, nil
}

func GetSize(fd int) (int, int, error) {
	return terminal.GetSize(fd)
}

func MakeRaw(fd int) (*State, error) {
	state, err := terminal.MakeRaw(fd)
	if err != nil {
		return nil, err
	}

	oldState := State(*state)
	return &oldState, nil
}

func Restore(fd int, state *State) error {
	newState := terminal.State(*state)

	return terminal.Restore(fd, &newState)
}
