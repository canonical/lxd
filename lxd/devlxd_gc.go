// +build gc

package main

import (
	"syscall"
)

func getUcred(fd int) (uint32, uint32, int32, error) {
	cred, err := syscall.GetsockoptUcred(fd, syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	if err != nil {
		return 0, 0, -1, err
	}

	return cred.Uid, cred.Gid, cred.Pid, nil
}
