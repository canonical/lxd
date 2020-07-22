// +build linux
// +build cgo

package shared

import (
	"fmt"
	"os"
	"unsafe"

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

// UserId is an adaption from https://codereview.appspot.com/4589049.
func UserId(name string) (int, error) {
	var pw C.struct_passwd
	var result *C.struct_passwd

	bufSize := C.sysconf(C._SC_GETPW_R_SIZE_MAX)
	if bufSize < 0 {
		bufSize = 4096
	}

	buf := C.malloc(C.size_t(bufSize))
	if buf == nil {
		return -1, fmt.Errorf("allocation failed")
	}
	defer C.free(buf)

	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

again:
	rv, errno := C.getpwnam_r(cname,
		&pw,
		(*C.char)(buf),
		C.size_t(bufSize),
		&result)
	if rv < 0 {
		// OOM killer will take care of us if we end up doing this too
		// often.
		if errno == unix.ERANGE {
			bufSize *= 2
			tmp := C.realloc(buf, C.size_t(bufSize))
			if tmp == nil {
				return -1, fmt.Errorf("allocation failed")
			}
			buf = tmp
			goto again
		}
		return -1, fmt.Errorf("failed user lookup: %s", unix.Errno(rv))
	}

	if result == nil {
		return -1, fmt.Errorf("unknown user %s", name)
	}

	return int(C.int(result.pw_uid)), nil
}

// GroupId is an adaption from https://codereview.appspot.com/4589049.
func GroupId(name string) (int, error) {
	var grp C.struct_group
	var result *C.struct_group

	bufSize := C.sysconf(C._SC_GETGR_R_SIZE_MAX)
	if bufSize < 0 {
		bufSize = 4096
	}

	buf := C.malloc(C.size_t(bufSize))
	if buf == nil {
		return -1, fmt.Errorf("allocation failed")
	}

	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

again:
	rv, errno := C.getgrnam_r(cname,
		&grp,
		(*C.char)(buf),
		C.size_t(bufSize),
		&result)
	if rv != 0 {
		// OOM killer will take care of us if we end up doing this too
		// often.
		if errno == unix.ERANGE {
			bufSize *= 2
			tmp := C.realloc(buf, C.size_t(bufSize))
			if tmp == nil {
				return -1, fmt.Errorf("allocation failed")
			}
			buf = tmp
			goto again
		}

		C.free(buf)
		return -1, fmt.Errorf("failed group lookup: %s", unix.Errno(rv))
	}
	C.free(buf)

	if result == nil {
		return -1, fmt.Errorf("unknown group %s", name)
	}

	return int(C.int(result.gr_gid)), nil
}

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
