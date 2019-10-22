// +build linux,cgo

package shared

// #cgo CFLAGS: -std=gnu11 -Wvla -Werror -fvisibility=hidden
// #cgo LDFLAGS: -lutil -lpthread
import "C"
