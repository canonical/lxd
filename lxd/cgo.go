// +build linux,cgo

package main

// #cgo CFLAGS: -std=gnu11 -Wvla -Werror -fvisibility=hidden
// #cgo pkg-config: lxc
// #cgo pkg-config: libcap
import "C"
