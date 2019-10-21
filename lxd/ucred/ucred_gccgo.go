// +build gccgo
// +build cgo

package ucred

import (
	"errors"
)

/*
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <sys/socket.h>
#include <sys/types.h>
#include <errno.h>
#include <stdio.h>
#include <string.h>

void getucred(int sock, uint *uid, uint *gid, int *pid)
{
	struct ucred peercred = {0};
	socklen_t len;

	len = sizeof(struct ucred);
	if (getsockopt(sock, SOL_SOCKET, SO_PEERCRED, &peercred, &len) != 0 || len != sizeof(peercred)) {
		fprintf(stderr, "getsockopt failed: %s\n", strerror(errno));
		return;
	}

	*uid = peercred.uid;
	*gid = peercred.gid;
	*pid = peercred.pid;

	return;
}
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
import "C"

// GetUCred returns the file descriptor's ucreds.
func GetUCred(fd int) (uint32, uint32, int32, error) {
	uid := C.uint(0)
	gid := C.uint(0)
	pid := C.int(-1)

	C.getucred(C.int(fd), &uid, &gid, &pid)

	if uid == 0 || gid == 0 || pid == -1 {
		return 0, 0, -1, errors.New("Failed to get the ucred")
	}

	return uint32(uid), uint32(gid), int32(pid), nil
}
