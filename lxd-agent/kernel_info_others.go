//go:build windows

package main

import (
	"fmt"
)

func kernelInfo() (name string, arch string, version string, err error) {
	return "", "", "", fmt.Errorf("Not implemented")
}
