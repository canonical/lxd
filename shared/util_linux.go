// +build linux

package shared

import (
	"bufio"
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

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/units"
)

// --- pure Go functions ---

func GetFileStat(p string) (uid int, gid int, major uint32, minor uint32, inode uint64, nlink int, err error) {
	var stat unix.Stat_t
	err = unix.Lstat(p, &stat)
	if err != nil {
		return
	}
	uid = int(stat.Uid)
	gid = int(stat.Gid)
	inode = uint64(stat.Ino)
	nlink = int(stat.Nlink)
	if stat.Mode&unix.S_IFBLK != 0 || stat.Mode&unix.S_IFCHR != 0 {
		major = unix.Major(uint64(stat.Rdev))
		minor = unix.Minor(uint64(stat.Rdev))
	}

	return
}

// GetPathMode returns a os.FileMode for the provided path
func GetPathMode(path string) (os.FileMode, error) {
	fi, err := os.Stat(path)
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
	// If we find a mount entry, it is obviously a mount point.
	ret := parseMountinfo(name)
	if ret == 1 {
		return true
	}

	// Get the stat details.
	stat, err := os.Stat(name)
	if err != nil {
		return false
	}

	rootStat, err := os.Lstat(name + "/..")
	if err != nil {
		return false
	}

	// If the directory has the same device as parent, then it's not a mountpoint.
	if stat.Sys().(*syscall.Stat_t).Dev == rootStat.Sys().(*syscall.Stat_t).Dev {
		return false
	}

	// Btrfs annoyingly uses a different Dev id for different subvolumes on the same mount.
	// So for btrfs, we require a matching mount entry in mountinfo.
	fs := unix.Statfs_t{}
	err = unix.Statfs(name, &fs)
	if err == nil && fs.Type == filesystemSuperMagicBtrfs {
		return false
	}

	return true
}

func SetSize(fd int, width int, height int) (err error) {
	var dimensions [4]uint16
	dimensions[0] = uint16(height)
	dimensions[1] = uint16(width)

	if _, _, err := unix.Syscall6(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCSWINSZ), uintptr(unsafe.Pointer(&dimensions)), 0, 0, 0); err != 0 {
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
	_p0, err = unix.BytePtrFromString(path)
	if err != nil {
		return
	}
	var _p1 unsafe.Pointer
	if len(list) > 0 {
		_p1 = unsafe.Pointer(&list[0])
	} else {
		_p1 = unsafe.Pointer(nil)
	}
	r0, _, e1 := unix.Syscall(unix.SYS_LLISTXATTR, uintptr(unsafe.Pointer(_p0)), uintptr(_p1), uintptr(len(list)))
	sz = int(r0)
	if e1 != 0 {
		err = e1
	}
	return
}

// GetAllXattr retrieves all extended attributes associated with a file,
// directory or symbolic link.
func GetAllXattr(path string) (xattrs map[string]string, err error) {
	e1 := fmt.Errorf("Extended attributes changed during retrieval")

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
		return nil, fmt.Errorf("No valid extended attribute key found")
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
		pre, err = unix.Getxattr(path, xattr, nil)
		if err != nil || pre < 0 {
			return nil, err
		}

		dest = make([]byte, pre)
		post := 0
		if pre > 0 {
			post, err = unix.Getxattr(path, xattr, dest)
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

var ObjectFound = fmt.Errorf("Found requested object")

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
		return "", fmt.Errorf("Failed to detect UUID: %s", err)
	}

	if uuid == "" {
		return "", fmt.Errorf("Failed to detect UUID")
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

	tmpErrno, ok := err.(unix.Errno)
	if ok {
		return tmpErrno, true
	}

	return nil, false
}

// Utsname returns the same info as unix.Utsname, as strings
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

	uname := unix.Utsname{}
	err := unix.Uname(&uname)
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

func Statvfs(path string) (*unix.Statfs_t, error) {
	var st unix.Statfs_t

	err := unix.Statfs(path, &st)
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

		// Feed the result to units.ParseByteSizeString to get an int value
		valueBytes, err := units.ParseByteSizeString(value)
		if err != nil {
			return -1, err
		}

		return valueBytes, nil
	}

	return -1, fmt.Errorf("Couldn't find MemTotal")
}

// OpenPtyInDevpts creates a new PTS pair, configures them and returns them.
func OpenPtyInDevpts(devpts_fd int, uid, gid int64) (*os.File, *os.File, error) {
	revert := true
	var ptx *os.File
	var err error

	// Create a PTS pair.
	if devpts_fd >= 0 {
		fd, err := unix.Openat(devpts_fd, "ptmx", os.O_RDWR|unix.O_CLOEXEC, 0)
		if err == nil {
			ptx = os.NewFile(uintptr(fd), "/dev/pts/ptmx")
		}
	} else {
		ptx, err = os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, nil, err
		}
	}
	defer func() {
		if revert {
			ptx.Close()
		}
	}()

	// Unlock the ptx and pty.
	val := 0
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(ptx.Fd()), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&val)))
	if errno != 0 {
		return nil, nil, unix.Errno(errno)
	}

	var pty *os.File
	ptyFd, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(ptx.Fd()), unix.TIOCGPTPEER, uintptr(unix.O_NOCTTY|unix.O_CLOEXEC|os.O_RDWR))
	// We can only fallback to looking up the fd in /dev/pts when we aren't dealing with the container's devpts instance.
	if errno == 0 {
		// Get the pty side.
		id := 0
		_, _, errno = unix.Syscall(unix.SYS_IOCTL, uintptr(ptx.Fd()), unix.TIOCGPTN, uintptr(unsafe.Pointer(&id)))
		if errno != 0 {
			return nil, nil, unix.Errno(errno)
		}

		pty = os.NewFile(ptyFd, fmt.Sprintf("/dev/pts/%d", id))
	} else {
		if devpts_fd >= 0 {
			return nil, nil, fmt.Errorf("TIOCGPTPEER required but not available")
		}

		// Get the pty side.
		id := 0
		_, _, errno = unix.Syscall(unix.SYS_IOCTL, uintptr(ptx.Fd()), unix.TIOCGPTN, uintptr(unsafe.Pointer(&id)))
		if errno != 0 {
			return nil, nil, unix.Errno(errno)
		}

		// Open the pty.
		pty, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", id), os.O_RDWR|unix.O_NOCTTY, 0)
		if err != nil {
			return nil, nil, err
		}
	}
	defer func() {
		if revert {
			pty.Close()
		}
	}()

	// Configure both sides
	for _, entry := range []*os.File{ptx, pty} {
		// Get termios.
		t, err := unix.IoctlGetTermios(int(entry.Fd()), unix.TCGETS)
		if err != nil {
			return nil, nil, err
		}

		// Set flags.
		t.Cflag |= unix.IMAXBEL
		t.Cflag |= unix.IUTF8
		t.Cflag |= unix.BRKINT
		t.Cflag |= unix.IXANY
		t.Cflag |= unix.HUPCL

		// Set termios.
		err = unix.IoctlSetTermios(int(entry.Fd()), unix.TCSETS, t)
		if err != nil {
			return nil, nil, err
		}

		// Set the default window size.
		sz := &unix.Winsize{
			Col: 80,
			Row: 25,
		}

		err = unix.IoctlSetWinsize(int(entry.Fd()), unix.TIOCSWINSZ, sz)
		if err != nil {
			return nil, nil, err
		}

		// Set CLOEXEC.
		_, _, errno = unix.Syscall(unix.SYS_FCNTL, uintptr(entry.Fd()), unix.F_SETFD, unix.FD_CLOEXEC)
		if errno != 0 {
			return nil, nil, unix.Errno(errno)
		}
	}

	// Fix the ownership of the pty side.
	err = unix.Fchown(int(pty.Fd()), int(uid), int(gid))
	if err != nil {
		return nil, nil, err
	}

	revert = false
	return ptx, pty, nil
}

// OpenPty creates a new PTS pair, configures them and returns them.
func OpenPty(uid, gid int64) (*os.File, *os.File, error) {
	return OpenPtyInDevpts(-1, uid, gid)
}

// Extensively commented directly in the code. Please leave the comments!
// Looking at this in a couple of months noone will know why and how this works
// anymore.
func ExecReaderToChannel(r io.Reader, bufferSize int, exited <-chan struct{}, fd int) <-chan []byte {
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

		ret, revents, err := GetPollRevents(fd, 0, (unix.POLLIN | unix.POLLPRI | unix.POLLERR | unix.POLLHUP | unix.POLLRDHUP | unix.POLLNVAL))
		if ret < 0 {
			logger.Errorf("Failed to poll(POLLIN | POLLPRI | POLLHUP | POLLRDHUP) on file descriptor: %s.", err)
			// Something went wrong so let's exited otherwise we
			// end up in an endless loop.
			once.Do(closeChannel)
		} else if ret > 0 {
			if (revents & unix.POLLERR) > 0 {
				logger.Warnf("Detected poll(POLLERR) event.")
				// Read end has likely been closed so again,
				// avoid an endless loop.
				once.Do(closeChannel)
			} else if (revents & unix.POLLNVAL) > 0 {
				logger.Warnf("Detected poll(POLLNVAL) event.")
				// Well, someone closed the fd havent they? So
				// let's go home.
				once.Do(closeChannel)
			}
		} else if ret == 0 {
			logger.Debugf("No data in stdout: exiting.")
			once.Do(closeChannel)
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

			ret, revents, err := GetPollRevents(fd, -1, (unix.POLLIN | unix.POLLPRI | unix.POLLERR | unix.POLLHUP | unix.POLLRDHUP | unix.POLLNVAL))
			if ret < 0 {
				// This condition is only reached in cases where we are massively f*cked since we even handle
				// EINTR in the underlying C wrapper around poll(). So let's exit here.
				logger.Errorf("Failed to poll(POLLIN | POLLPRI | POLLERR | POLLHUP | POLLRDHUP) on file descriptor: %s. Exiting.", err)
				return
			}

			// [2]: If the process exits before all its data has been read by us and no other process holds stdin or
			// stdout open, then we will observe a (POLLHUP | POLLRDHUP | POLLIN) event. This means, we need to
			// keep on reading from the pty file descriptor until we get a simple POLLHUP back.
			both := ((revents & (unix.POLLIN | unix.POLLPRI)) > 0) && ((revents & (unix.POLLHUP | unix.POLLRDHUP)) > 0)
			if both {
				logger.Debugf("Detected poll(POLLIN | POLLPRI | POLLHUP | POLLRDHUP) event.")
				read := buf[offset : offset+readSize]
				nr, err = r.Read(read)
			}

			if (revents & unix.POLLERR) > 0 {
				logger.Warnf("Detected poll(POLLERR) event: exiting.")
				return
			} else if (revents & unix.POLLNVAL) > 0 {
				logger.Warnf("Detected poll(POLLNVAL) event: exiting.")
				return
			}

			if ((revents & (unix.POLLIN | unix.POLLPRI)) > 0) && !both {
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
					ret, revents, err := GetPollRevents(fd, 0, (unix.POLLIN | unix.POLLPRI | unix.POLLERR | unix.POLLHUP | unix.POLLRDHUP | unix.POLLNVAL))
					if ret < 0 {
						logger.Errorf("Failed to poll(POLLIN | POLLPRI | POLLERR | POLLHUP | POLLRDHUP) on file descriptor: %s. Exiting.", err)
						return
					} else if (revents & (unix.POLLHUP | unix.POLLRDHUP | unix.POLLERR | unix.POLLNVAL)) == 0 {
						logger.Debugf("Exiting but background processes are still running.")
						return
					}
				}
				read := buf[offset : offset+readSize]
				nr, err = r.Read(read)
			}

			// The attached process has exited and we have read all data that may have
			// been buffered.
			if ((revents & (unix.POLLHUP | unix.POLLRDHUP)) > 0) && !both {
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

// GetPollRevents poll for events on provided fd.
func GetPollRevents(fd int, timeout int, flags int) (int, int, error) {
	pollFd := unix.PollFd{
		Fd:      int32(fd),
		Events:  int16(flags),
		Revents: 0,
	}
	pollFds := []unix.PollFd{pollFd}

again:
	n, err := unix.Poll(pollFds, timeout)
	if err != nil {
		if err == syscall.EAGAIN || err == syscall.EINTR {
			goto again
		}

		return -1, -1, err
	}

	return n, int(pollFds[0].Revents), err
}
