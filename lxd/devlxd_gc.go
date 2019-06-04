// +build gc

package main

import "golang.org/x/sys/unix"

func getUcred(fd int) (uint32, uint32, int32, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, 0, -1, err
	}

	return cred.Uid, cred.Gid, cred.Pid, nil
}
