// +build linux
// +build cgo

package shared

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/lxc/lxd/shared/logger"
)

// #cgo LDFLAGS: -lutil -lpthread
/*
#define _GNU_SOURCE
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

#ifndef AT_SYMLINK_FOLLOW
#define AT_SYMLINK_FOLLOW    0x400
#endif

#ifndef AT_EMPTY_PATH
#define AT_EMPTY_PATH       0x1000
#endif

#define ABSTRACT_UNIX_SOCK_LEN sizeof(((struct sockaddr_un *)0)->sun_path)

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

void create_pty(int *master, int *slave, uid_t uid, gid_t gid) {
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

int get_poll_revents(int lfd, int timeout, int flags, int *revents, int *saved_errno)
{
	int ret;
	struct pollfd pfd = {lfd, flags, 0};

again:
	ret = poll(&pfd, 1, timeout);
	if (ret < 0) {
		if (errno == EINTR)
			goto again;

		*saved_errno = errno;
		fprintf(stderr, "Failed to poll() on file descriptor.\n");
		return -1;
	}

	*revents = pfd.revents;

	return ret;
}

int lxc_abstract_unix_send_fds(int fd, int *sendfds, int num_sendfds,
			       void *data, size_t size)
{
	int ret;
	struct msghdr msg;
	struct iovec iov;
	struct cmsghdr *cmsg = NULL;
	char buf[1] = {0};
	char *cmsgbuf;
	size_t cmsgbufsize = CMSG_SPACE(num_sendfds * sizeof(int));

	memset(&msg, 0, sizeof(msg));
	memset(&iov, 0, sizeof(iov));

	cmsgbuf = malloc(cmsgbufsize);
	if (!cmsgbuf)
		return -1;

	msg.msg_control = cmsgbuf;
	msg.msg_controllen = cmsgbufsize;

	cmsg = CMSG_FIRSTHDR(&msg);
	cmsg->cmsg_level = SOL_SOCKET;
	cmsg->cmsg_type = SCM_RIGHTS;
	cmsg->cmsg_len = CMSG_LEN(num_sendfds * sizeof(int));

	msg.msg_controllen = cmsg->cmsg_len;

	memcpy(CMSG_DATA(cmsg), sendfds, num_sendfds * sizeof(int));

	iov.iov_base = data ? data : buf;
	iov.iov_len = data ? size : sizeof(buf);
	msg.msg_iov = &iov;
	msg.msg_iovlen = 1;

	ret = sendmsg(fd, &msg, MSG_NOSIGNAL);
	if (ret < 0)
		fprintf(stderr, "%s - Failed to send file descriptor\n", strerror(errno));
	free(cmsgbuf);
	return ret;
}

int lxc_abstract_unix_recv_fds(int fd, int *recvfds, int num_recvfds,
			       void *data, size_t size)
{
	int ret;
	struct msghdr msg;
	struct iovec iov;
	struct cmsghdr *cmsg = NULL;
	char buf[1] = {0};
	char *cmsgbuf;
	size_t cmsgbufsize = CMSG_SPACE(num_recvfds * sizeof(int));

	memset(&msg, 0, sizeof(msg));
	memset(&iov, 0, sizeof(iov));

	cmsgbuf = malloc(cmsgbufsize);
	if (!cmsgbuf)
		return -1;

	msg.msg_control = cmsgbuf;
	msg.msg_controllen = cmsgbufsize;

	iov.iov_base = data ? data : buf;
	iov.iov_len = data ? size : sizeof(buf);
	msg.msg_iov = &iov;
	msg.msg_iovlen = 1;

	ret = recvmsg(fd, &msg, 0);
	if (ret <= 0) {
		fprintf(stderr, "%s - Failed to receive file descriptor\n", strerror(errno));
		goto out;
	}

	cmsg = CMSG_FIRSTHDR(&msg);

	memset(recvfds, -1, num_recvfds * sizeof(int));
	if (cmsg && cmsg->cmsg_len == CMSG_LEN(num_recvfds * sizeof(int)) &&
	    cmsg->cmsg_level == SOL_SOCKET && cmsg->cmsg_type == SCM_RIGHTS) {
		memcpy(recvfds, CMSG_DATA(cmsg), num_recvfds * sizeof(int));
	}

out:
	free(cmsgbuf);
	return ret;
}
*/
import "C"

const ABSTRACT_UNIX_SOCK_LEN int = C.ABSTRACT_UNIX_SOCK_LEN

const POLLIN int = C.POLLIN
const POLLPRI int = C.POLLPRI
const POLLNVAL int = C.POLLNVAL
const POLLERR int = C.POLLERR
const POLLHUP int = C.POLLHUP
const POLLRDHUP int = C.POLLRDHUP

func GetPollRevents(fd int, timeout int, flags int) (int, int, error) {
	var err error
	revents := C.int(0)
	saved_errno := C.int(0)

	ret := C.get_poll_revents(C.int(fd), C.int(timeout), C.int(flags), &revents, &saved_errno)
	if int(ret) < 0 {
		err = syscall.Errno(saved_errno)
	}

	return int(ret), int(revents), err
}

func AbstractUnixSendFd(sockFD int, sendFD int) error {
	fd := C.int(sendFD)
	sk_fd := C.int(sockFD)
	ret := C.lxc_abstract_unix_send_fds(sk_fd, &fd, C.int(1), nil, C.size_t(0))
	if ret < 0 {
		return fmt.Errorf("Failed to send file descriptor via abstract unix socket")
	}

	return nil
}

func AbstractUnixReceiveFd(sockFD int) (*os.File, error) {
	fd := C.int(-1)
	sk_fd := C.int(sockFD)
	ret := C.lxc_abstract_unix_recv_fds(sk_fd, &fd, C.int(1), nil, C.size_t(0))
	if ret < 0 {
		return nil, fmt.Errorf("Failed to receive file descriptor via abstract unix socket")
	}

	file := os.NewFile(uintptr(fd), "")
	return file, nil
}

func OpenPty(uid, gid int64) (master *os.File, slave *os.File, err error) {
	fd_master := C.int(-1)
	fd_slave := C.int(-1)
	rootUid := C.uid_t(uid)
	rootGid := C.gid_t(gid)

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
		if errno == syscall.ERANGE {
			bufSize *= 2
			tmp := C.realloc(buf, C.size_t(bufSize))
			if tmp == nil {
				return -1, fmt.Errorf("allocation failed")
			}
			buf = tmp
			goto again
		}
		return -1, fmt.Errorf("failed user lookup: %s", syscall.Errno(rv))
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
		if errno == syscall.ERANGE {
			bufSize *= 2
			tmp := C.realloc(buf, C.size_t(bufSize))
			if tmp == nil {
				return -1, fmt.Errorf("allocation failed")
			}
			buf = tmp
			goto again
		}

		C.free(buf)
		return -1, fmt.Errorf("failed group lookup: %s", syscall.Errno(rv))
	}
	C.free(buf)

	if result == nil {
		return -1, fmt.Errorf("unknown group %s", name)
	}

	return int(C.int(result.gr_gid)), nil
}

// --- pure Go functions ---

func Major(dev uint64) int {
	return int(((dev >> 8) & 0xfff) | ((dev >> 32) & (0xfffff000)))
}

func Minor(dev uint64) int {
	return int((dev & 0xff) | ((dev >> 12) & (0xffffff00)))
}

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
		major = Major(stat.Rdev)
		minor = Minor(stat.Rdev)
	}

	return
}

// FileCopy copies a file, overwriting the target if it exists.
func GetPathMode(path string) (os.FileMode, error) {
	s, err := os.Open(path)
	if err != nil {
		return os.FileMode(0000), err
	}
	defer s.Close()

	fi, err := s.Stat()
	if err != nil {
		return os.FileMode(0000), err
	}

	mode, _, _ := GetOwnerMode(fi)
	return mode, nil
}

func parseMountinfo(name string) int {
	// In case someone uses symlinks we need to look for the actual
	// mountpoint.
	actualPath, err := filepath.EvalSymlinks(name)
	if err != nil {
		return -1
	}

	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return -1
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		tokens := strings.Fields(line)
		if len(tokens) < 5 {
			return -1
		}
		cleanPath := filepath.Clean(tokens[4])
		if cleanPath == actualPath {
			return 1
		}
	}

	return 0
}

func IsMountPoint(name string) bool {
	ret := parseMountinfo(name)
	if ret == 1 {
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

		dest = make([]byte, pre)
		post := 0
		if pre > 0 {
			post, err = syscall.Getxattr(path, xattr, dest)
			if err != nil || post < 0 {
				return nil, err
			}
		}

		if post != pre {
			return nil, e1
		}

		xattrs[xattr] = string(dest)
	}

	return xattrs, nil
}

// Extensively commented directly in the code. Please leave the comments!
// Looking at this in a couple of months noone will know why and how this works
// anymore.
func ExecReaderToChannel(r io.Reader, bufferSize int, exited <-chan bool, fd int) <-chan []byte {
	if bufferSize <= (128 * 1024) {
		bufferSize = (128 * 1024)
	}

	ch := make(chan ([]byte))

	// Takes care that the closeChannel() function is exactly executed once.
	// This allows us to avoid using a mutex.
	var once sync.Once
	closeChannel := func() {
		close(ch)
	}

	// [1]: This function has just one job: Dealing with the case where we
	// are running an interactive shell session where we put a process in
	// the background that does hold stdin/stdout open, but does not
	// generate any output at all. This case cannot be dealt with in the
	// following function call. Here's why: Assume the above case, now the
	// attached child (the shell in this example) exits. This will not
	// generate any poll() event: We won't get POLLHUP because the
	// background process is holding stdin/stdout open and noone is writing
	// to it. So we effectively block on GetPollRevents() in the function
	// below. Hence, we use another go routine here who's only job is to
	// handle that case: When we detect that the child has exited we check
	// whether a POLLIN or POLLHUP event has been generated. If not, we know
	// that there's nothing buffered on stdout and exit.
	var attachedChildIsDead int32 = 0
	go func() {
		<-exited

		atomic.StoreInt32(&attachedChildIsDead, 1)

		ret, revents, err := GetPollRevents(fd, 0, (POLLIN | POLLPRI | POLLERR | POLLHUP | POLLRDHUP | POLLNVAL))
		if ret < 0 {
			logger.Errorf("Failed to poll(POLLIN | POLLPRI | POLLHUP | POLLRDHUP) on file descriptor: %s.", err)
		} else if ret > 0 {
			if (revents & POLLERR) > 0 {
				logger.Warnf("Detected poll(POLLERR) event.")
			} else if (revents & POLLNVAL) > 0 {
				logger.Warnf("Detected poll(POLLNVAL) event.")
			}
		} else if ret == 0 {
			logger.Debugf("No data in stdout: exiting.")
			once.Do(closeChannel)
			return
		}
	}()

	go func() {
		readSize := (128 * 1024)
		offset := 0
		buf := make([]byte, bufferSize)
		avoidAtomicLoad := false

		defer once.Do(closeChannel)
		for {
			nr := 0
			var err error

			ret, revents, err := GetPollRevents(fd, -1, (POLLIN | POLLPRI | POLLERR | POLLHUP | POLLRDHUP | POLLNVAL))
			if ret < 0 {
				// This condition is only reached in cases where we are massively f*cked since we even handle
				// EINTR in the underlying C wrapper around poll(). So let's exit here.
				logger.Errorf("Failed to poll(POLLIN | POLLPRI | POLLERR | POLLHUP | POLLRDHUP) on file descriptor: %s. Exiting.", err)
				return
			}

			// [2]: If the process exits before all its data has been read by us and no other process holds stdin or
			// stdout open, then we will observe a (POLLHUP | POLLRDHUP | POLLIN) event. This means, we need to
			// keep on reading from the pty file descriptor until we get a simple POLLHUP back.
			both := ((revents & (POLLIN | POLLPRI)) > 0) && ((revents & (POLLHUP | POLLRDHUP)) > 0)
			if both {
				logger.Debugf("Detected poll(POLLIN | POLLPRI | POLLHUP | POLLRDHUP) event.")
				read := buf[offset : offset+readSize]
				nr, err = r.Read(read)
			}

			if (revents & POLLERR) > 0 {
				logger.Warnf("Detected poll(POLLERR) event: exiting.")
				return
			} else if (revents & POLLNVAL) > 0 {
				logger.Warnf("Detected poll(POLLNVAL) event: exiting.")
				return
			}

			if ((revents & (POLLIN | POLLPRI)) > 0) && !both {
				// This might appear unintuitive at first but is actually a nice trick: Assume we are running
				// a shell session in a container and put a process in the background that is writing to
				// stdout. Now assume the attached process (aka the shell in this example) exits because we
				// used Ctrl+D to send EOF or something. If no other process would be holding stdout open we
				// would expect to observe either a (POLLHUP | POLLRDHUP | POLLIN | POLLPRI) event if there
				// is still data buffered from the previous process or a simple (POLLHUP | POLLRDHUP) if
				// no data is buffered. The fact that we only observe a (POLLIN | POLLPRI) event means that
				// another process is holding stdout open and is writing to it.
				// One counter argument that can be leveraged is (brauner looks at tycho :))
				// "Hey, you need to write at least one additional tty buffer to make sure that
				// everything that the attached child has written is actually shown."
				// The answer to that is:
				// "This case can only happen if the process has exited and has left data in stdout which
				// would generate a (POLLIN | POLLPRI | POLLHUP | POLLRDHUP) event and this case is already
				// handled and triggers another codepath. (See [2].)"
				if avoidAtomicLoad || atomic.LoadInt32(&attachedChildIsDead) == 1 {
					avoidAtomicLoad = true
					// Handle race between atomic.StorInt32() in the go routine
					// explained in [1] and atomic.LoadInt32() in the go routine
					// here:
					// We need to check for (POLLHUP | POLLRDHUP) here again since we might
					// still be handling a pure POLLIN event from a write prior to the childs
					// exit. But the child might have exited right before and performed
					// atomic.StoreInt32() to update attachedChildIsDead before we
					// performed our atomic.LoadInt32(). This means we accidentally hit this
					// codepath and are misinformed about the available poll() events. So we
					// need to perform a non-blocking poll() again to exclude that case:
					//
					// - If we detect no (POLLHUP | POLLRDHUP) event we know the child
					//   has already exited but someone else is holding stdin/stdout open and
					//   writing to it.
					//   Note that his case should only ever be triggered in situations like
					//   running a shell and doing stuff like:
					//    > ./lxc exec xen1 -- bash
					//   root@xen1:~# yes &
					//   .
					//   .
					//   .
					//   now send Ctrl+D or type "exit". By the time the Ctrl+D/exit event is
					//   triggered, we will have read all of the childs data it has written to
					//   stdout and so we can assume that anything that comes now belongs to
					//   the process that is holding stdin/stdout open.
					//
					// - If we detect a (POLLHUP | POLLRDHUP) event we know that we've
					//   hit this codepath on accident caused by the race between
					//   atomic.StoreInt32() in the go routine explained in [1] and
					//   atomic.LoadInt32() in this go routine. So the next call to
					//   GetPollRevents() will either return
					//   (POLLIN | POLLPRI | POLLERR | POLLHUP | POLLRDHUP)
					//   or (POLLHUP | POLLRDHUP). Both will trigger another codepath (See [2].)
					//   that takes care that all data of the child that is buffered in
					//   stdout is written out.
					ret, revents, err := GetPollRevents(fd, 0, (POLLIN | POLLPRI | POLLERR | POLLHUP | POLLRDHUP | POLLNVAL))
					if ret < 0 {
						logger.Errorf("Failed to poll(POLLIN | POLLPRI | POLLERR | POLLHUP | POLLRDHUP) on file descriptor: %s. Exiting.", err)
						return
					} else if (revents & (POLLHUP | POLLRDHUP | POLLERR | POLLNVAL)) == 0 {
						logger.Debugf("Exiting but background processes are still running.")
						return
					}
				}
				read := buf[offset : offset+readSize]
				nr, err = r.Read(read)
			}

			// The attached process has exited and we have read all data that may have
			// been buffered.
			if ((revents & (POLLHUP | POLLRDHUP)) > 0) && !both {
				logger.Debugf("Detected poll(POLLHUP) event: exiting.")
				return
			}

			offset += nr
			if offset > 0 && (offset+readSize >= bufferSize || err != nil) {
				ch <- buf[0:offset]
				offset = 0
				buf = make([]byte, bufferSize)
			}
		}
	}()

	return ch
}

var ObjectFound = fmt.Errorf("Found requested object.")

func LookupUUIDByBlockDevPath(diskDevice string) (string, error) {
	uuid := ""
	readUUID := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if (info.Mode() & os.ModeSymlink) == os.ModeSymlink {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}

			// filepath.Join() will call Clean() on the result and
			// thus resolve those ugly "../../" parts that make it
			// hard to compare the strings.
			absPath := filepath.Join("/dev/disk/by-uuid", link)
			if absPath == diskDevice {
				uuid = path
				// Will allows us to avoid needlessly travers
				// the whole directory.
				return ObjectFound
			}
		}
		return nil
	}

	err := filepath.Walk("/dev/disk/by-uuid", readUUID)
	if err != nil && err != ObjectFound {
		return "", fmt.Errorf("Failed to detect UUID: %s.", err)
	}

	if uuid == "" {
		return "", fmt.Errorf("Failed to detect UUID.")
	}

	lastSlash := strings.LastIndex(uuid, "/")
	return uuid[lastSlash+1:], nil
}

// Detect whether err is an errno.
func GetErrno(err error) (errno error, iserrno bool) {
	sysErr, ok := err.(*os.SyscallError)
	if ok {
		return sysErr.Err, true
	}

	pathErr, ok := err.(*os.PathError)
	if ok {
		return pathErr.Err, true
	}

	tmpErrno, ok := err.(syscall.Errno)
	if ok {
		return tmpErrno, true
	}

	return nil, false
}

// Utsname returns the same info as syscall.Utsname, as strings
type Utsname struct {
	Sysname    string
	Nodename   string
	Release    string
	Version    string
	Machine    string
	Domainname string
}

// Uname returns Utsname as strings
func Uname() (*Utsname, error) {
	/*
	 * Based on: https://groups.google.com/forum/#!topic/golang-nuts/Jel8Bb-YwX8
	 * there is really no better way to do this, which is
	 * unfortunate. Also, we ditch the more accepted CharsToString
	 * version in that thread, since it doesn't seem as portable,
	 * viz. github issue #206.
	 */

	uname := syscall.Utsname{}
	err := syscall.Uname(&uname)
	if err != nil {
		return nil, err
	}

	return &Utsname{
		Sysname:    intArrayToString(uname.Sysname),
		Nodename:   intArrayToString(uname.Nodename),
		Release:    intArrayToString(uname.Release),
		Version:    intArrayToString(uname.Version),
		Machine:    intArrayToString(uname.Machine),
		Domainname: intArrayToString(uname.Domainname),
	}, nil
}

func intArrayToString(arr interface{}) string {
	slice := reflect.ValueOf(arr)
	s := ""
	for i := 0; i < slice.Len(); i++ {
		val := slice.Index(i)
		valInt := int64(-1)

		switch val.Kind() {
		case reflect.Int:
		case reflect.Int8:
			valInt = int64(val.Int())
		case reflect.Uint:
		case reflect.Uint8:
			valInt = int64(val.Uint())
		default:
			continue
		}

		if valInt == 0 {
			break
		}

		s += string(byte(valInt))
	}

	return s
}

func Statvfs(path string) (*syscall.Statfs_t, error) {
	var st syscall.Statfs_t

	err := syscall.Statfs(path, &st)
	if err != nil {
		return nil, err
	}

	return &st, nil
}

func DeviceTotalMemory() (int64, error) {
	// Open /proc/meminfo
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return -1, err
	}
	defer f.Close()

	// Read it line by line
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()

		// We only care about MemTotal
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}

		// Extract the before last (value) and last (unit) fields
		fields := strings.Split(line, " ")
		value := fields[len(fields)-2] + fields[len(fields)-1]

		// Feed the result to shared.ParseByteSizeString to get an int value
		valueBytes, err := ParseByteSizeString(value)
		if err != nil {
			return -1, err
		}

		return valueBytes, nil
	}

	return -1, fmt.Errorf("Couldn't find MemTotal")
}
