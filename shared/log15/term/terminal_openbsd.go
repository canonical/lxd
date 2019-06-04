package term

import "golang.org/x/sys/unix"

const ioctlReadTermios = unix.TIOCGETA

type Termios unix.Termios
