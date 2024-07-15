//go:build linux

package shared

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unsafe"

	"github.com/pkg/xattr"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/shared/revert"
	"github.com/canonical/lxd/shared/units"
)

// --- pure Go functions ---

// GetFileStat retrieves the UID, GID, major and minor device numbers, inode, and number of hard links for
// the given file path.
func GetFileStat(p string) (uid int, gid int, major uint32, minor uint32, inode uint64, nlink int, err error) {
	var stat unix.Stat_t
	err = unix.Lstat(p, &stat)
	if err != nil {
		return 0, 0, 0, 0, 0, 0, err
	}

	uid = int(stat.Uid)
	gid = int(stat.Gid)
	inode = uint64(stat.Ino)
	nlink = int(stat.Nlink)
	if stat.Mode&unix.S_IFBLK != 0 || stat.Mode&unix.S_IFCHR != 0 {
		major = unix.Major(uint64(stat.Rdev))
		minor = unix.Minor(uint64(stat.Rdev))
	}

	return uid, gid, major, minor, inode, nlink, nil
}

// GetPathMode returns a os.FileMode for the provided path.
func GetPathMode(path string) (os.FileMode, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return os.FileMode(0000), err
	}

	mode, _, _ := GetOwnerMode(fi)
	return mode, nil
}

// SetSize sets the terminal size to the specified width and height for the given file descriptor.
func SetSize(fd int, width int, height int) (err error) {
	var dimensions [4]uint16
	dimensions[0] = uint16(height)
	dimensions[1] = uint16(width)

	_, _, errno := unix.Syscall6(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCSWINSZ), uintptr(unsafe.Pointer(&dimensions)), 0, 0, 0)
	if errno != 0 {
		return errno
	}

	return nil
}

// GetAllXattr retrieves all extended attributes associated with a file, directory or symbolic link.
func GetAllXattr(path string) (map[string]string, error) {
	xattrNames, err := xattr.LList(path)
	if err != nil {
		// Some filesystems don't support llistxattr() for various reasons.
		// Interpret this as a set of no xattrs, instead of an error.
		if errors.Is(err, unix.EOPNOTSUPP) {
			return nil, nil
		}

		return nil, fmt.Errorf("Failed getting extended attributes from %q: %w", path, err)
	}

	var xattrs = make(map[string]string, len(xattrNames))
	for _, xattrName := range xattrNames {
		value, err := xattr.LGet(path, xattrName)
		if err != nil {
			return nil, fmt.Errorf("Failed getting %q extended attribute from %q: %w", xattrName, path, err)
		}

		xattrs[xattrName] = string(value)
	}

	return xattrs, nil
}

// ErrObjectFound indicates that the requested object was found.
var ErrObjectFound = fmt.Errorf("Found requested object")

// LookupUUIDByBlockDevPath finds and returns the UUID of a block device by its path.
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
				return ErrObjectFound
			}
		}
		return nil
	}

	err := filepath.Walk("/dev/disk/by-uuid", readUUID)
	if err != nil && err != ErrObjectFound {
		return "", fmt.Errorf("Failed to detect UUID: %s", err)
	}

	if uuid == "" {
		return "", fmt.Errorf("Failed to detect UUID")
	}

	lastSlash := strings.LastIndex(uuid, "/")
	return uuid[lastSlash+1:], nil
}

// GetErrno detects whether the error is an errno.
//
//revive:disable:error-return Error is returned first because this is similar to assertion.
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

// Utsname returns the same info as unix.Utsname, as strings.
type Utsname struct {
	Sysname    string
	Nodename   string
	Release    string
	Version    string
	Machine    string
	Domainname string
}

// Uname returns Utsname as strings.
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

func intArrayToString(arr any) string {
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

// DeviceTotalMemory returns the total memory of the device by reading /proc/meminfo.
func DeviceTotalMemory() (int64, error) {
	return GetMeminfo("MemTotal")
}

// GetMeminfo retrieves the memory information for the specified field from /proc/meminfo.
func GetMeminfo(field string) (int64, error) {
	// Open /proc/meminfo
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return -1, err
	}

	defer func() { _ = f.Close() }()

	// Read it line by line
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()

		// We only care about MemTotal
		if !strings.HasPrefix(line, field+":") {
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

	return -1, fmt.Errorf("Couldn't find %s", field)
}

// OpenPtyInDevpts creates a new PTS pair, configures them and returns them.
func OpenPtyInDevpts(devptsFD int, uid, gid int64) (*os.File, *os.File, error) {
	revert := revert.New()
	defer revert.Fail()
	var fd int
	var ptx *os.File
	var err error

	// Create a PTS pair.
	if devptsFD >= 0 {
		fd, err = unix.Openat(devptsFD, "ptmx", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOCTTY, 0)
	} else {
		fd, err = unix.Openat(-1, "/dev/ptmx", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOCTTY, 0)
	}

	if err != nil {
		return nil, nil, err
	}

	ptx = os.NewFile(uintptr(fd), "/dev/pts/ptmx")
	revert.Add(func() { _ = ptx.Close() })

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
		if devptsFD >= 0 {
			return nil, nil, fmt.Errorf("TIOCGPTPEER required but not available")
		}

		// Get the pty side.
		id := 0
		_, _, errno = unix.Syscall(unix.SYS_IOCTL, uintptr(ptx.Fd()), unix.TIOCGPTN, uintptr(unsafe.Pointer(&id)))
		if errno != 0 {
			return nil, nil, unix.Errno(errno)
		}

		// Open the pty.
		pty, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", id), unix.O_NOCTTY|unix.O_CLOEXEC|os.O_RDWR, 0)
		if err != nil {
			return nil, nil, err
		}
	}
	revert.Add(func() { _ = pty.Close() })

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

	revert.Success()
	return ptx, pty, nil
}

// OpenPty creates a new PTS pair, configures them and returns them.
func OpenPty(uid, gid int64) (*os.File, *os.File, error) {
	return OpenPtyInDevpts(-1, uid, gid)
}

// ExitStatus extracts the exit status from the error returned by exec.Cmd.
// If a nil err is provided then an exit status of 0 is returned along with the nil error.
// If a valid exit status can be extracted from err then it is returned along with a nil error.
// If no valid exit status can be extracted then a -1 exit status is returned along with the err provided.
func ExitStatus(err error) (int, error) {
	if err == nil {
		return 0, err // No error exit status.
	}

	var exitErr *exec.ExitError

	// Detect and extract ExitError to check the embedded exit status.
	if errors.As(err, &exitErr) {
		// If the process was signaled, extract the signal.
		status, isWaitStatus := exitErr.Sys().(unix.WaitStatus)
		if isWaitStatus && status.Signaled() {
			return 128 + int(status.Signal()), nil // 128 + n == Fatal error signal "n"
		}

		// Otherwise capture the exit status from the command.
		return exitErr.ExitCode(), nil
	}

	return -1, err // Not able to extract an exit status.
}

// GetPollRevents poll for events on provided fd.
func GetPollRevents(fd int, timeout int, flags int) (n int, revents int, err error) {
	pollFd := unix.PollFd{
		Fd:      int32(fd),
		Events:  int16(flags),
		Revents: 0,
	}

	pollFds := []unix.PollFd{pollFd}

again:
	n, err = unix.Poll(pollFds, timeout)
	if err != nil {
		if err == unix.EAGAIN || err == unix.EINTR {
			goto again
		}

		return -1, -1, err
	}

	return n, int(pollFds[0].Revents), err
}

// NewExecWrapper returns a new ReadWriteCloser wrapper for an os.File.
// The ctx is used to indicate when the executed process has ended, at which point any further Read calls will
// return io.EOF rather than potentially blocking on the poll syscall if the process is a shell that still has
// background processes running that are not producing any output.
func NewExecWrapper(ctx context.Context, f *os.File) io.ReadWriteCloser {
	return &execWrapper{
		ctx: ctx,
		f:   f,
	}
}

// execWrapper implements a ReadWriteCloser wrapper for an os.File connected to a PTY.
type execWrapper struct {
	f              *os.File
	ctx            context.Context
	finishDeadline time.Time
}

// Read uses the poll syscall with a timeout of 1s to check if there is any data to read.
// This avoids potentially blocking in the poll syscall in situations where the process is a shell that has
// background processes that are not producing any output.
// If the ctx has been cancelled before the poll starts then io.EOF error is returned.
func (w *execWrapper) Read(p []byte) (int, error) {
	rawConn, err := w.f.SyscallConn()
	if err != nil {
		return 0, err
	}

	var opErr error
	var n int
	err = rawConn.Read(func(fd uintptr) bool {
		for {
			// Call poll() with 1s timeout, this prevents blocking if a shell process exits leaving
			// background processes running that are not outputting anything.
			_, revents, err := GetPollRevents(int(fd), 1000, (unix.POLLIN | unix.POLLPRI | unix.POLLERR | unix.POLLNVAL | unix.POLLHUP | unix.POLLRDHUP))

			switch {
			case err != nil:
				opErr = err
			case revents&unix.POLLERR > 0:
				opErr = fmt.Errorf("Got POLLERR event")
			case revents&unix.POLLNVAL > 0:
				opErr = fmt.Errorf("Got POLLNVAL event")
			case revents&(unix.POLLIN|unix.POLLPRI) > 0:
				// If there is something to read then read it.
				n, opErr = unix.Read(int(fd), p)
				if opErr == nil && w.ctx.Err() != nil {
					if w.finishDeadline.IsZero() {
						// When the parent process finishes set a deadline to complete
						// future reads by.
						w.finishDeadline = time.Now().Add(time.Second)
					} else if time.Now().After(w.finishDeadline) {
						// If there is still output being received after the parent
						// process has finished then return EOF to prevent background
						// processes from keeping the reads ongoing.
						opErr = io.EOF
					}
				}
			case w.ctx.Err() != nil:
				// Nothing to read after process exited then return EOF.
				opErr = io.EOF
			default:
				continue
			}

			return true
		}
	})
	if err != nil {
		return n, err
	}

	return n, opErr
}

// Write writes data to the underlying os.File.
func (w *execWrapper) Write(p []byte) (int, error) {
	return w.f.Write(p)
}

// Close closes the underlying os.File.
func (w *execWrapper) Close() error {
	return w.f.Close()
}
