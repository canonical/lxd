// +build darwin dragonfly freebsd netbsd openbsd

package termios

import (
	"golang.org/x/sys/unix"
)

const ioctlReadTermios = unix.TIOCGETA
