// +build linux,cgo,gccgo

package ucred

// #cgo CFLAGS: -std=gnu11 -Wvla -Werror -fvisibility=hidden -Winit-self
// #cgo CFLAGS: -Wformat=2 -Wshadow -Wendif-labels -fasynchronous-unwind-tables
// #cgo CFLAGS: -pipe -Wcast-align --param=ssp-buffer-size=4 -g -Wunused
// #cgo CFLAGS: -Werror=implicit-function-declaration
// #cgo CFLAGS: -Werror=return-type -Wendif-labels -Werror=overflow
// #cgo CFLAGS: -Wnested-externs -fexceptions
import "C"
