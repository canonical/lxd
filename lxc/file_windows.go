// +build windows

package main

import (
	"os"
)

func (c *fileCmd) getOwner(f *os.File) (os.FileMode, int, int, error) {
	return os.FileMode(0), -1, -1, nil
}
