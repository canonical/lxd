// +build linux,cgo

package seccomp

// #cgo CFLAGS: -std=gnu11 -Wvla -Werror -fvisibility=hidden
import "C"
