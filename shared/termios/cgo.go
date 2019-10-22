// +build !windows,cgo

package termios

// #cgo CFLAGS: -std=gnu11 -Wvla -Werror -fvisibility=hidden
import "C"
