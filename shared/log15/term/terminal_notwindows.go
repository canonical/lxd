// Based on ssh/terminal:
// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build linux,!appengine darwin freebsd openbsd

package term

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// IsTty returns true if the given file descriptor is a terminal.
func IsTty(fd uintptr) bool {
	var termios Termios
	_, _, err := unix.Syscall6(unix.SYS_IOCTL, fd, ioctlReadTermios, uintptr(unsafe.Pointer(&termios)), 0, 0, 0)
	return err == 0
}
