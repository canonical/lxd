// +build linux,cgo

package idmap

// #cgo CFLAGS: -std=gnu11 -Wvla -Werror -fvisibility=hidden
import "C"
