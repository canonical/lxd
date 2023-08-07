//go:build !linux || !cgo

package endpoints

import (
	"fmt"
	"net"
)

// LocalCreateListener is a stub function returning an error indicating unsupported platform.
func localCreateListener(path string, group string) (net.Listener, error) {
	return nil, fmt.Errorf("Platform isn't supported")
}

// CreateDevLxdlListener is a stub function returning an error indicating unsupported platform.
func createDevLxdlListener(path string) (net.Listener, error) {
	return nil, fmt.Errorf("Platform isn't supported")
}
