// +build linux,cgo

package netutils

// #cgo CFLAGS: -std=gnu11 -Wvla -Werror -fvisibility=hidden
import "C"
