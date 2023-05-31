//go:build !windows

package main

import (
	"github.com/lxc/lxd/shared"
)

func kernelInfo() (name string, arch string, version string, err error) {
	uname, err := shared.Uname()
	if err != nil {
		return "", "", "", err
	}

	return uname.Sysname, uname.Machine, uname.Release, nil
}
