// +build linux
// +build cgo

package shared

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

// #cgo LDFLAGS: -lutil -lpthread
/*
#define _GNU_SOURCE
#include <sys/types.h>
#include <sys/stat.h>
#include <unistd.h>
#include <stdlib.h>
#include <grp.h>
#include <pty.h>
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <poll.h>
#include <string.h>
#include <stdio.h>

#ifndef AT_SYMLINK_FOLLOW
#define AT_SYMLINK_FOLLOW    0x400
#endif

#ifndef AT_EMPTY_PATH
#define AT_EMPTY_PATH       0x1000
#endif

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
		fprintf(stderr, "Failed to get settings: %s\n", strerror(errno));
		return;
	}

	term_settings.c_iflag |= IMAXBEL;
	term_settings.c_iflag |= IUTF8;
	term_settings.c_iflag |= BRKINT;
	term_settings.c_iflag |= IXANY;

	term_settings.c_cflag |= HUPCL;

	if (tcsetattr(fd, TCSANOW, &term_settings) < 0) {
		fprintf(stderr, "Failed to set settings: %s\n", strerror(errno));
		return;
	}

	if (ioctl(fd, TIOCGWINSZ, &win) < 0) {
		fprintf(stderr, "Failed to get the terminal size: %s\n", strerror(errno));
		return;
	}

	win.ws_col = 80;
	win.ws_row = 25;

	if (ioctl(fd, TIOCSWINSZ, &win) < 0) {
		fprintf(stderr, "Failed to set the terminal size: %s\n", strerror(errno));
		return;
	}

	if (fcntl(fd, F_SETFD, FD_CLOEXEC) < 0) {
		fprintf(stderr, "Failed to set FD_CLOEXEC: %s\n", strerror(errno));
		return;
	}

	return;
}

void create_pty(int *master, int *slave, int uid, int gid) {
	if (openpty(master, slave, NULL, NULL, NULL) < 0) {
		fprintf(stderr, "Failed to openpty: %s\n", strerror(errno));
		return;
	}

	configure_pty(*master);
	configure_pty(*slave);

	if (fchown(*slave, uid, gid) < 0) {
		fprintf(stderr, "Warning: error chowning pty to container root\n");
		fprintf(stderr, "Continuing...\n");
	}
	if (fchown(*master, uid, gid) < 0) {
		fprintf(stderr, "Warning: error chowning pty to container root\n");
		fprintf(stderr, "Continuing...\n");
	}
}

void create_pipe(int *master, int *slave) {
	int pipefd[2];

	if (pipe2(pipefd, O_CLOEXEC) < 0) {
		fprintf(stderr, "Failed to create a pipe: %s\n", strerror(errno));
		return;
	}

	*master = pipefd[0];
	*slave = pipefd[1];
}

int shiftowner(char *basepath, char *path, int uid, int gid) {
	struct stat sb;
	int fd, r;
	char fdpath[PATH_MAX];
	char realpath[PATH_MAX];

	fd = open(path, O_PATH|O_NOFOLLOW);
	if (fd < 0 ) {
		perror("Failed open");
		return 1;
	}

	r = sprintf(fdpath, "/proc/self/fd/%d", fd);
	if (r < 0) {
		perror("Failed sprintf");
		close(fd);
		return 1;
	}

	r = readlink(fdpath, realpath, PATH_MAX);
	if (r < 0) {
		perror("Failed readlink");
		close(fd);
		return 1;
	}

	if (strlen(realpath) < strlen(basepath)) {
		printf("Invalid path, source (%s) is outside of basepath (%s).\n", realpath, basepath);
		close(fd);
		return 1;
	}

	if (strncmp(realpath, basepath, strlen(basepath))) {
		printf("Invalid path, source (%s) is outside of basepath (%s).\n", realpath, basepath);
		close(fd);
		return 1;
	}

	r = fstat(fd, &sb);
	if (r < 0) {
		perror("Failed fstat");
		close(fd);
		return 1;
	}

	r = fchownat(fd, "", uid, gid, AT_EMPTY_PATH|AT_SYMLINK_NOFOLLOW);
	if (r < 0) {
		perror("Failed chown");
		close(fd);
		return 1;
	}

	if (!S_ISLNK(sb.st_mode)) {
		r = chmod(fdpath, sb.st_mode);
		if (r < 0) {
			perror("Failed chmod");
			close(fd);
			return 1;
		}
	}

	close(fd);
	return 0;
}

int fd_has_data(int lfd)
{
	struct pollfd pfd = {lfd, POLLIN, 0};
	int ret;

	ret = poll(&pfd, 1, 0);
	if (ret < 0)
		fprintf(stderr, "Failed to poll() on file descriptor.\n");

	return ret;
}
*/
import "C"

func FdHasData(fd int) int {
	return int(C.fd_has_data(C.int(fd)))
}

func ShiftOwner(basepath string, path string, uid int, gid int) error {
	cbasepath := C.CString(basepath)
	defer C.free(unsafe.Pointer(cbasepath))

	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	r := C.shiftowner(cbasepath, cpath, C.int(uid), C.int(gid))
	if r != 0 {
		return fmt.Errorf("Failed to change ownership of: %s", path)
	}
	return nil
}

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
		return "", fmt.Errorf("allocation failed")
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
		return "", fmt.Errorf("failed group lookup: %s", syscall.Errno(rv))
	}

	if result == nil {
		return "", fmt.Errorf("unknown group %d", gid)
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
		return -1, fmt.Errorf("allocation failed")
	}
	defer C.free(buf)

	// mygetgrgid_r is a wrapper around getgrgid_r to
	// to avoid using gid_t because C.gid_t(gid) for
	// unknown reasons doesn't work on linux.
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	rv := C.getgrnam_r(cname,
		&grp,
		(*C.char)(buf),
		bufSize,
		&result)

	if rv != 0 {
		return -1, fmt.Errorf("failed group lookup: %s", syscall.Errno(rv))
	}

	if result == nil {
		return -1, fmt.Errorf("unknown group %s", name)
	}

	return int(C.int(result.gr_gid)), nil
}

// --- pure Go functions ---

func GetFileStat(p string) (uid int, gid int, major int, minor int,
	inode uint64, nlink int, err error) {
	var stat syscall.Stat_t
	err = syscall.Lstat(p, &stat)
	if err != nil {
		return
	}
	uid = int(stat.Uid)
	gid = int(stat.Gid)
	inode = uint64(stat.Ino)
	nlink = int(stat.Nlink)
	major = -1
	minor = -1
	if stat.Mode&syscall.S_IFBLK != 0 || stat.Mode&syscall.S_IFCHR != 0 {
		major = int(stat.Rdev / 256)
		minor = int(stat.Rdev % 256)
	}

	return
}

func IsMountPoint(name string) bool {
	_, err := exec.LookPath("mountpoint")
	if err == nil {
		err = exec.Command("mountpoint", "-q", name).Run()
		if err != nil {
			return false
		}

		return true
	}

	stat, err := os.Stat(name)
	if err != nil {
		return false
	}

	rootStat, err := os.Lstat(name + "/..")
	if err != nil {
		return false
	}
	// If the directory has the same device as parent, then it's not a mountpoint.
	return stat.Sys().(*syscall.Stat_t).Dev != rootStat.Sys().(*syscall.Stat_t).Dev
}

func ReadLastNLines(f *os.File, lines int) (string, error) {
	if lines <= 0 {
		return "", fmt.Errorf("invalid line count")
	}

	stat, err := f.Stat()
	if err != nil {
		return "", err
	}

	data, err := syscall.Mmap(int(f.Fd()), 0, int(stat.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return "", err
	}
	defer syscall.Munmap(data)

	for i := len(data) - 1; i >= 0; i-- {
		if data[i] == '\n' {
			lines--
		}

		if lines < 0 {
			return string(data[i+1 : len(data)]), nil
		}
	}

	return string(data), nil
}

func SetSize(fd int, width int, height int) (err error) {
	var dimensions [4]uint16
	dimensions[0] = uint16(height)
	dimensions[1] = uint16(width)

	if _, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TIOCSWINSZ), uintptr(unsafe.Pointer(&dimensions)), 0, 0, 0); err != 0 {
		return err
	}
	return nil
}

// This uses ssize_t llistxattr(const char *path, char *list, size_t size); to
// handle symbolic links (should it in the future be possible to set extended
// attributed on symlinks): If path is a symbolic link the extended attributes
// associated with the link itself are retrieved.
func llistxattr(path string, list []byte) (sz int, err error) {
	var _p0 *byte
	_p0, err = syscall.BytePtrFromString(path)
	if err != nil {
		return
	}
	var _p1 unsafe.Pointer
	if len(list) > 0 {
		_p1 = unsafe.Pointer(&list[0])
	} else {
		_p1 = unsafe.Pointer(nil)
	}
	r0, _, e1 := syscall.Syscall(syscall.SYS_LLISTXATTR, uintptr(unsafe.Pointer(_p0)), uintptr(_p1), uintptr(len(list)))
	sz = int(r0)
	if e1 != 0 {
		err = e1
	}
	return
}

// GetAllXattr retrieves all extended attributes associated with a file,
// directory or symbolic link.
func GetAllXattr(path string) (xattrs map[string]string, err error) {
	e1 := fmt.Errorf("Extended attributes changed during retrieval.")

	// Call llistxattr() twice: First, to determine the size of the buffer
	// we need to allocate to store the extended attributes, second, to
	// actually store the extended attributes in the buffer. Also, check if
	// the size/number of extended attributes hasn't changed between the two
	// calls.
	pre, err := llistxattr(path, nil)
	if err != nil || pre < 0 {
		return nil, err
	}
	if pre == 0 {
		return nil, nil
	}

	dest := make([]byte, pre)

	post, err := llistxattr(path, dest)
	if err != nil || post < 0 {
		return nil, err
	}
	if post != pre {
		return nil, e1
	}

	split := strings.Split(string(dest), "\x00")
	if split == nil {
		return nil, fmt.Errorf("No valid extended attribute key found.")
	}
	// *listxattr functions return a list of  names  as  an unordered array
	// of null-terminated character strings (attribute names are separated
	// by null bytes ('\0')), like this: user.name1\0system.name1\0user.name2\0
	// Since we split at the '\0'-byte the last element of the slice will be
	// the empty string. We remove it:
	if split[len(split)-1] == "" {
		split = split[:len(split)-1]
	}

	xattrs = make(map[string]string, len(split))

	for _, x := range split {
		xattr := string(x)
		// Call Getxattr() twice: First, to determine the size of the
		// buffer we need to allocate to store the extended attributes,
		// second, to actually store the extended attributes in the
		// buffer. Also, check if the size of the extended attribute
		// hasn't changed between the two calls.
		pre, err = syscall.Getxattr(path, xattr, nil)
		if err != nil || pre < 0 {
			return nil, err
		}
		if pre == 0 {
			return nil, fmt.Errorf("No valid extended attribute value found.")
		}

		dest = make([]byte, pre)
		post, err = syscall.Getxattr(path, xattr, dest)
		if err != nil || post < 0 {
			return nil, err
		}
		if post != pre {
			return nil, e1
		}

		xattrs[xattr] = string(dest)
	}

	return xattrs, nil
}
