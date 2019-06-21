package termios

import (
	"golang.org/x/sys/unix"
)

const ioctlReadTermios = unix.TCGETS
