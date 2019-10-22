// +build linux,cgo,gccgo

package ucred

// #cgo CFLAGS: -std=gnu11 -Wvla -Werror -fvisibility=hidden
import "C"
