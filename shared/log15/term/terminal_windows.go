// Based on ssh/terminal:
// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build windows

package term

import (
	"golang.org/x/sys/windows"
)

var kernel32 = windows.NewLazyDLL("kernel32.dll")

// IsTty returns true if the given file descriptor is a terminal.
func IsTty(fd uintptr) bool {
	var mode uint32
	err := windows.GetConsoleMode(windows.Handle(fd), &mode)
	return mode != 0 && err == nil
}
