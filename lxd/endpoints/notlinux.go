//go:build !linux || !cgo

package endpoints

import (
	"fmt"
	"net"
)

func localCreateListener(path string, group string) (net.Listener, error) {
	return nil, fmt.Errorf("Platform is not supported")
}

func createDevLxdlListener(path string) (net.Listener, error) {
	return nil, fmt.Errorf("Platform is not supported")
}
