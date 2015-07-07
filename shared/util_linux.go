// +build linux
// +build cgo

package shared

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/chai2010/gettext-go/gettext"
)

// #cgo LDFLAGS: -lutil -lpthread
/*
#include <unistd.h>
#include <stdlib.h>
#include <sys/types.h>
#include <grp.h>
#include <pty.h>
#include <errno.h>
#include <fcntl.h>
#include <string.h>
#include <stdio.h>

// This is an adaption from https://codereview.appspot.com/4589049, to be
// included in the stdlib with the stdlib's license.

static int mygetgrgid_r(int gid, struct group *grp,
	char *buf, size_t buflen, struct group **result) {
	return getgrgid_r(gid, grp, buf, buflen, result);
}

void configure_pty(int fd) {
	struct termios term_settings;
	struct winsize win;

	if (tcgetattr(fd, &term_settings) < 0) {
		printf("Failed to get settings: %s\n", strerror(errno));
		return;
	}

	term_settings.c_iflag |= IMAXBEL;
	term_settings.c_iflag |= IUTF8;
	term_settings.c_iflag |= BRKINT;
	term_settings.c_iflag |= IXANY;

	term_settings.c_cflag |= HUPCL;

	if (tcsetattr(fd, TCSANOW, &term_settings) < 0) {
		printf("Failed to set settings: %s\n", strerror(errno));
		return;
	}

	if (ioctl(fd, TIOCGWINSZ, &win) < 0) {
		printf("Failed to get the terminal size: %s\n", strerror(errno));
		return;
	}

	win.ws_col = 80;
	win.ws_row = 25;

	if (ioctl(fd, TIOCSWINSZ, &win) < 0) {
		printf("Failed to set the terminal size: %s\n", strerror(errno));
		return;
	}

	if (fcntl(fd, F_SETFD, FD_CLOEXEC) < 0) {
		printf("Failed to set FD_CLOEXEC: %s\n", strerror(errno));
		return;
	}

	return;
}

void create_pty(int *master, int *slave, int uid, int gid) {
	if (openpty(master, slave, NULL, NULL, NULL) < 0) {
		printf("Failed to openpty: %s\n", strerror(errno));
		return;
	}

	configure_pty(*master);
	configure_pty(*slave);

	if (fchown(*slave, uid, gid) < 0) {
		printf("Warning: error chowning pty to container root\n");
		printf("Continuing...\n");
	}
	if (fchown(*master, uid, gid) < 0) {
		printf("Warning: error chowning pty to container root\n");
		printf("Continuing...\n");
	}
}

void create_pipe(int *master, int *slave) {
	int pipefd[2];

	if (pipe2(pipefd, O_CLOEXEC) < 0) {
		printf("Failed to create a pipe: %s\n", strerror(errno));
		return;
	}

	*master = pipefd[0];
	*slave = pipefd[1];
}

*/
import "C"

func OpenPty(uid, gid int) (master *os.File, slave *os.File, err error) {
	fd_master := C.int(-1)
	fd_slave := C.int(-1)
	rootUid := C.int(uid)
	rootGid := C.int(gid)

	C.create_pty(&fd_master, &fd_slave, rootUid, rootGid)

	if fd_master == -1 || fd_slave == -1 {
		return nil, nil, errors.New("Failed to create a new pts pair")
	}

	master = os.NewFile(uintptr(fd_master), "master")
	slave = os.NewFile(uintptr(fd_slave), "slave")

	return master, slave, nil
}

func Pipe() (master *os.File, slave *os.File, err error) {
	fd_master := C.int(-1)
	fd_slave := C.int(-1)

	C.create_pipe(&fd_master, &fd_slave)

	if fd_master == -1 || fd_slave == -1 {
		return nil, nil, errors.New("Failed to create a new pipe")
	}

	master = os.NewFile(uintptr(fd_master), "master")
	slave = os.NewFile(uintptr(fd_slave), "slave")

	return master, slave, nil
}

// GroupName is an adaption from https://codereview.appspot.com/4589049.
func GroupName(gid int) (string, error) {
	var grp C.struct_group
	var result *C.struct_group

	bufSize := C.size_t(C.sysconf(C._SC_GETGR_R_SIZE_MAX))
	buf := C.malloc(bufSize)
	if buf == nil {
		return "", fmt.Errorf(gettext.Gettext("allocation failed"))
	}
	defer C.free(buf)

	// mygetgrgid_r is a wrapper around getgrgid_r to
	// to avoid using gid_t because C.gid_t(gid) for
	// unknown reasons doesn't work on linux.
	rv := C.mygetgrgid_r(C.int(gid),
		&grp,
		(*C.char)(buf),
		bufSize,
		&result)

	if rv != 0 {
		return "", fmt.Errorf(gettext.Gettext("failed group lookup: %s"), syscall.Errno(rv))
	}

	if result == nil {
		return "", fmt.Errorf(gettext.Gettext("unknown group %s"), gid)
	}

	return C.GoString(result.gr_name), nil
}

// GroupId is an adaption from https://codereview.appspot.com/4589049.
func GroupId(name string) (int, error) {
	var grp C.struct_group
	var result *C.struct_group

	bufSize := C.size_t(C.sysconf(C._SC_GETGR_R_SIZE_MAX))
	buf := C.malloc(bufSize)
	if buf == nil {
		return -1, fmt.Errorf(gettext.Gettext("allocation failed"))
	}
	defer C.free(buf)

	// mygetgrgid_r is a wrapper around getgrgid_r to
	// to avoid using gid_t because C.gid_t(gid) for
	// unknown reasons doesn't work on linux.
	rv := C.getgrnam_r(C.CString(name),
		&grp,
		(*C.char)(buf),
		bufSize,
		&result)

	if rv != 0 {
		return -1, fmt.Errorf(gettext.Gettext("failed group lookup: %s"), syscall.Errno(rv))
	}

	if result == nil {
		return -1, fmt.Errorf(gettext.Gettext("unknown group %s"), name)
	}

	return int(C.int(result.gr_gid)), nil
}
