// +build linux
// +build cgo

package shared

import (
	"fmt"
	"os"

	// Used by cgo
	_ "github.com/lxc/lxd/lxd/include"

	"golang.org/x/sys/unix"
)

/*
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <errno.h>
#include <fcntl.h>
#include <grp.h>
#include <limits.h>
#include <poll.h>
#include <pty.h>
#include <pwd.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/stat.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <sys/un.h>

#include "../lxd/include/process_utils.h"

#define ABSTRACT_UNIX_SOCK_LEN sizeof(((struct sockaddr_un *)0)->sun_path)

static int read_pid(int fd)
{
	ssize_t ret;
	pid_t n = -1;

again:
	ret = read(fd, &n, sizeof(n));
	if (ret < 0 && errno == EINTR)
		goto again;

	if (ret < 0)
		return -1;

	return n;
}
*/
import "C"

const ABSTRACT_UNIX_SOCK_LEN int = C.ABSTRACT_UNIX_SOCK_LEN

func ReadPid(r *os.File) int {
	return int(C.read_pid(C.int(r.Fd())))
}

func unCloexec(fd int) error {
	var err error = nil
	flags, _, errno := unix.Syscall(unix.SYS_FCNTL, uintptr(fd), unix.F_GETFD, 0)
	if errno != 0 {
		err = errno
		return err
	}

	flags &^= unix.FD_CLOEXEC
	_, _, errno = unix.Syscall(unix.SYS_FCNTL, uintptr(fd), unix.F_SETFD, flags)
	if errno != 0 {
		err = errno
	}
	return err
}

func PidFdOpen(Pid int, Flags uint32) (*os.File, error) {
	pidFd, errno := C.pidfd_open(C.int(Pid), C.uint32_t(Flags))
	if errno != nil {
		return nil, errno
	}

	errno = unCloexec(int(pidFd))
	if errno != nil {
		return nil, errno
	}

	return os.NewFile(uintptr(pidFd), fmt.Sprintf("%d", Pid)), nil
}

func PidfdSendSignal(Pidfd int, Signal int, Flags uint32) error {
	ret, errno := C.pidfd_send_signal(C.int(Pidfd), C.int(Signal), nil, C.uint32_t(Flags))
	if ret != 0 {
		return errno
	}

	return nil
}
