// +build linux
// +build cgo

package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/netutils"
)

/*
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <elf.h>
#include <errno.h>
#include <fcntl.h>
#include <linux/seccomp.h>
#include <linux/types.h>
#include <linux/kdev_t.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <sys/sysmacros.h>
#include <sys/types.h>
#include <unistd.h>

#ifndef SECCOMP_GET_NOTIF_SIZES
#define SECCOMP_GET_NOTIF_SIZES 3
#endif

#ifndef SECCOMP_RET_USER_NOTIF
#define SECCOMP_RET_USER_NOTIF 0x7fc00000U

struct seccomp_notif_sizes {
	__u16 seccomp_notif;
	__u16 seccomp_notif_resp;
	__u16 seccomp_data;
};

struct seccomp_notif {
	__u64 id;
	__u32 pid;
	__u32 flags;
	struct seccomp_data data;
};

struct seccomp_notif_resp {
	__u64 id;
	__s64 val;
	__s32 error;
	__u32 flags;
};

#endif // !SECCOMP_RET_USER_NOTIF

struct seccomp_notif_sizes expected_sizes;

struct seccomp_notify_proxy_msg {
	uint64_t __reserved;
	pid_t monitor_pid;
	pid_t init_pid;
	struct seccomp_notif_sizes sizes;
	uint64_t cookie_len;
	// followed by: seccomp_notif, seccomp_notif_resp, cookie
};

#define SECCOMP_PROXY_MSG_SIZE (sizeof(struct seccomp_notify_proxy_msg))
#define SECCOMP_NOTIFY_SIZE (sizeof(struct seccomp_notif))
#define SECCOMP_RESPONSE_SIZE (sizeof(struct seccomp_notif_resp))
#define SECCOMP_MSG_SIZE_MIN (SECCOMP_PROXY_MSG_SIZE + SECCOMP_NOTIFY_SIZE + SECCOMP_RESPONSE_SIZE)
#define SECCOMP_COOKIE_SIZE (64 * sizeof(char))
#define SECCOMP_MSG_SIZE_MAX (SECCOMP_MSG_SIZE_MIN + SECCOMP_COOKIE_SIZE)

#ifdef SECCOMP_RET_USER_NOTIF

static int seccomp_notify_get_sizes(struct seccomp_notif_sizes *sizes)
{
	if (syscall(SYS_seccomp, SECCOMP_GET_NOTIF_SIZES, 0, sizes) != 0)
		return -1;

	if (sizes->seccomp_notif != sizeof(struct seccomp_notif) ||
	    sizes->seccomp_notif_resp != sizeof(struct seccomp_notif_resp) ||
	    sizes->seccomp_data != sizeof(struct seccomp_data))
		return -1;

	return 0;
}

static int device_allowed(dev_t dev, mode_t mode)
{
	if ((dev == makedev(0, 0)) && (mode & S_IFCHR)) // whiteout
		return 0;

	if ((dev == makedev(5, 1)) && (mode & S_IFCHR)) // /dev/console
		return 0;

	if ((dev == makedev(1, 7)) && (mode & S_IFCHR)) // /dev/full
		return 0;

	if ((dev == makedev(1, 3)) && (mode & S_IFCHR)) // /dev/null
		return 0;

	if ((dev == makedev(1, 8)) && (mode & S_IFCHR)) // /dev/random
		return 0;

	if ((dev == makedev(5, 0)) && (mode & S_IFCHR)) // /dev/tty
		return 0;

	if ((dev == makedev(1, 9)) && (mode & S_IFCHR)) // /dev/urandom
		return 0;

	if ((dev == makedev(1, 5)) && (mode & S_IFCHR)) // /dev/zero
		return 0;

	return -EPERM;
}

#include <linux/audit.h>

struct lxd_seccomp_data_arch {
	int arch;
	int nr_mknod;
	int nr_mknodat;
	int nr_setxattr;
};

#define LXD_SECCOMP_NOTIFY_MKNOD    0
#define LXD_SECCOMP_NOTIFY_MKNODAT  1
#define LXD_SECCOMP_NOTIFY_SETXATTR 2

// ordered by likelihood of usage...
static const struct lxd_seccomp_data_arch seccomp_notify_syscall_table[] = {
	{ -1, LXD_SECCOMP_NOTIFY_MKNOD, LXD_SECCOMP_NOTIFY_MKNODAT, LXD_SECCOMP_NOTIFY_SETXATTR },
#ifdef AUDIT_ARCH_X86_64
	{ AUDIT_ARCH_X86_64,      133, 259, 188 },
#endif
#ifdef AUDIT_ARCH_I386
	{ AUDIT_ARCH_I386,         14, 297, 226 },
#endif
#ifdef AUDIT_ARCH_AARCH64
	{ AUDIT_ARCH_AARCH64,      -1,  33,   5 },
#endif
#ifdef AUDIT_ARCH_ARM
	{ AUDIT_ARCH_ARM,          14, 324, 226 },
#endif
#ifdef AUDIT_ARCH_ARMEB
	{ AUDIT_ARCH_ARMEB,        14, 324, 226 },
#endif
#ifdef AUDIT_ARCH_S390
	{ AUDIT_ARCH_S390,         14, 290, 224 },
#endif
#ifdef AUDIT_ARCH_S390X
	{ AUDIT_ARCH_S390X,        14, 290, 224 },
#endif
#ifdef AUDIT_ARCH_PPC
	{ AUDIT_ARCH_PPC,          14, 288, 209 },
#endif
#ifdef AUDIT_ARCH_PPC64
	{ AUDIT_ARCH_PPC64,        14, 288, 209 },
#endif
#ifdef AUDIT_ARCH_PPC64LE
	{ AUDIT_ARCH_PPC64LE,      14, 288, 209 },
#endif
#ifdef AUDIT_ARCH_SPARC
	{ AUDIT_ARCH_SPARC,        14, 286, 169 },
#endif
#ifdef AUDIT_ARCH_SPARC64
	{ AUDIT_ARCH_SPARC64,      14, 286, 169 },
#endif
#ifdef AUDIT_ARCH_MIPS
	{ AUDIT_ARCH_MIPS,         14, 290, 224 },
#endif
#ifdef AUDIT_ARCH_MIPSEL
	{ AUDIT_ARCH_MIPSEL,       14, 290, 224 },
#endif
#ifdef AUDIT_ARCH_MIPS64
	{ AUDIT_ARCH_MIPS64,      131, 249, 180 },
#endif
#ifdef AUDIT_ARCH_MIPS64N32
	{ AUDIT_ARCH_MIPS64N32,   131, 253, 180 },
#endif
#ifdef AUDIT_ARCH_MIPSEL64
	{ AUDIT_ARCH_MIPSEL64,    131, 249, 180 },
#endif
#ifdef AUDIT_ARCH_MIPSEL64N32
	{ AUDIT_ARCH_MIPSEL64N32, 131, 253, 180 },
#endif
};

static int seccomp_notify_get_syscall(struct seccomp_notif *req,
				      struct seccomp_notif_resp *resp)
{
	resp->id = req->id;
	resp->flags = req->flags;
	resp->val = 0;
	resp->error = 0;

	for (size_t i = 0; i < (sizeof(seccomp_notify_syscall_table) /
				sizeof(seccomp_notify_syscall_table[0]));
	     i++) {
		const struct lxd_seccomp_data_arch *entry = &seccomp_notify_syscall_table[i];

		if (entry->arch != req->data.arch)
			continue;

		if (entry->nr_mknod == req->data.nr)
			return LXD_SECCOMP_NOTIFY_MKNOD;

		if (entry->nr_mknodat == req->data.nr)
			return LXD_SECCOMP_NOTIFY_MKNODAT;

		if (entry->nr_setxattr == req->data.nr)
			return LXD_SECCOMP_NOTIFY_SETXATTR;

		break;
	}

	errno = EINVAL;
	return -EINVAL;
}

static void seccomp_notify_update_response(struct seccomp_notif_resp *resp,
					   int new_neg_errno)
{
	resp->error = new_neg_errno;
}

static void prepare_seccomp_iovec(struct iovec *iov,
				  struct seccomp_notify_proxy_msg *msg,
				  struct seccomp_notif *notif,
				  struct seccomp_notif_resp *resp, char *cookie)
{
	iov[0].iov_base = msg;
	iov[0].iov_len = SECCOMP_PROXY_MSG_SIZE;

	iov[1].iov_base = notif;
	iov[1].iov_len = SECCOMP_NOTIFY_SIZE;

	iov[2].iov_base = resp;
	iov[2].iov_len = SECCOMP_RESPONSE_SIZE;

	iov[3].iov_base = cookie;
	iov[3].iov_len = SECCOMP_COOKIE_SIZE;
}
#endif // SECCOMP_RET_USER_NOTIF
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
import "C"

const LxdSeccompNotifyMknod = C.LXD_SECCOMP_NOTIFY_MKNOD
const LxdSeccompNotifyMknodat = C.LXD_SECCOMP_NOTIFY_MKNODAT
const LxdSeccompNotifySetxattr = C.LXD_SECCOMP_NOTIFY_SETXATTR

type SeccompServer struct {
	d    *Daemon
	path string
	l    net.Listener
}

type SeccompIovec struct {
	ucred  *ucred
	memFd  int
	procFd int
	msg    *C.struct_seccomp_notify_proxy_msg
	req    *C.struct_seccomp_notif
	resp   *C.struct_seccomp_notif_resp
	cookie *C.char
	iov    *C.struct_iovec
}

func NewSeccompIovec(ucred *ucred) *SeccompIovec {
	msg_ptr := C.malloc(C.sizeof_struct_seccomp_notify_proxy_msg)
	msg := (*C.struct_seccomp_notify_proxy_msg)(msg_ptr)
	C.memset(msg_ptr, 0, C.sizeof_struct_seccomp_notify_proxy_msg)

	req_ptr := C.malloc(C.sizeof_struct_seccomp_notif)
	req := (*C.struct_seccomp_notif)(req_ptr)
	C.memset(req_ptr, 0, C.sizeof_struct_seccomp_notif)

	resp_ptr := C.malloc(C.sizeof_struct_seccomp_notif_resp)
	resp := (*C.struct_seccomp_notif_resp)(resp_ptr)
	C.memset(resp_ptr, 0, C.sizeof_struct_seccomp_notif_resp)

	cookie_ptr := C.malloc(64 * C.sizeof_char)
	cookie := (*C.char)(cookie_ptr)
	C.memset(cookie_ptr, 0, 64*C.sizeof_char)

	iov_unsafe_ptr := C.malloc(4 * C.sizeof_struct_iovec)
	iov := (*C.struct_iovec)(iov_unsafe_ptr)
	C.memset(iov_unsafe_ptr, 0, 4*C.sizeof_struct_iovec)

	C.prepare_seccomp_iovec(iov, msg, req, resp, cookie)

	return &SeccompIovec{
		memFd:  -1,
		procFd: -1,
		msg:    msg,
		req:    req,
		resp:   resp,
		cookie: cookie,
		iov:    iov,
		ucred:  ucred,
	}
}

func (siov *SeccompIovec) PutSeccompIovec() {
	if siov.memFd >= 0 {
		unix.Close(siov.memFd)
	}
	if siov.procFd >= 0 {
		unix.Close(siov.procFd)
	}
	C.free(unsafe.Pointer(siov.msg))
	C.free(unsafe.Pointer(siov.req))
	C.free(unsafe.Pointer(siov.resp))
	C.free(unsafe.Pointer(siov.cookie))
	C.free(unsafe.Pointer(siov.iov))
}

func (siov *SeccompIovec) ReceiveSeccompIovec(fd int) (uint64, error) {
	bytes, fds, err := netutils.AbstractUnixReceiveFdData(fd, 2, unsafe.Pointer(siov.iov), 4)
	if err != nil || err == io.EOF {
		return 0, err
	}

	if len(fds) == 2 {
		siov.procFd = int(fds[0])
		siov.memFd = int(fds[1])
	} else {
		siov.memFd = int(fds[0])
	}

	return bytes, nil
}

func (siov *SeccompIovec) IsValidSeccompIovec(size uint64) bool {
	if size < uint64(C.SECCOMP_MSG_SIZE_MIN) {
		logger.Warnf("Disconnected from seccomp socket after incomplete receive")
		return false
	}
	if siov.msg.__reserved != 0 {
		logger.Warnf("Disconnected from seccomp socket after client sent non-zero reserved field: pid=%v",
			siov.ucred.pid)
		return false
	}

	if siov.msg.sizes.seccomp_notif != C.expected_sizes.seccomp_notif {
		logger.Warnf("Disconnected from seccomp socket since client uses different seccomp_notif sizes: %d != %d, pid=%v",
			siov.msg.sizes.seccomp_notif, C.expected_sizes.seccomp_notif, siov.ucred.pid)
		return false
	}

	if siov.msg.sizes.seccomp_notif_resp != C.expected_sizes.seccomp_notif_resp {
		logger.Warnf("Disconnected from seccomp socket since client uses different seccomp_notif_resp sizes: %d != %d, pid=%v",
			siov.msg.sizes.seccomp_notif_resp, C.expected_sizes.seccomp_notif_resp, siov.ucred.pid)
		return false
	}

	if siov.msg.sizes.seccomp_data != C.expected_sizes.seccomp_data {
		logger.Warnf("Disconnected from seccomp socket since client uses different seccomp_data sizes: %d != %d, pid=%v",
			siov.msg.sizes.seccomp_data, C.expected_sizes.seccomp_data, siov.ucred.pid)
		return false
	}

	return true
}

func (siov *SeccompIovec) SendSeccompIovec(fd int, errno int) error {
	C.seccomp_notify_update_response(siov.resp, C.int(errno))

	msghdr := C.struct_msghdr{}
	msghdr.msg_iov = siov.iov
	msghdr.msg_iovlen = 4 - 1 // without cookie
retry:
	bytes, err := C.sendmsg(C.int(fd), &msghdr, C.MSG_NOSIGNAL)
	if bytes < 0 {
		if err == unix.EINTR {
			logger.Debugf("Caught EINTR, retrying...")
			goto retry
		}
		logger.Debugf("Disconnected from seccomp socket after failed write for process %v: %s", siov.ucred.pid, err)
		return fmt.Errorf("Failed to send response to seccomp client %v", siov.ucred.pid)
	}

	if uint64(bytes) != uint64(C.SECCOMP_MSG_SIZE_MIN) {
		logger.Debugf("Disconnected from seccomp socket after short write: pid=%v", siov.ucred.pid)
		return fmt.Errorf("Failed to send full response to seccomp client %v", siov.ucred.pid)
	}

	return nil
}

func NewSeccompServer(d *Daemon, path string) (*SeccompServer, error) {
	ret := C.seccomp_notify_get_sizes(&C.expected_sizes)
	if ret < 0 {
		return nil, fmt.Errorf("Failed to query kernel for seccomp notifier sizes")
	}

	// Cleanup existing sockets
	if shared.PathExists(path) {
		err := os.Remove(path)
		if err != nil {
			return nil, err
		}
	}

	// Bind new socket
	l, err := net.Listen("unixpacket", path)
	if err != nil {
		return nil, err
	}

	// Restrict access
	err = os.Chmod(path, 0700)
	if err != nil {
		return nil, err
	}

	// Start the server
	s := SeccompServer{
		d:    d,
		path: path,
		l:    l,
	}

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}

			go func() {
				ucred, err := getCred(c.(*net.UnixConn))
				if err != nil {
					logger.Errorf("Unable to get ucred from seccomp socket client: %v", err)
					return
				}

				logger.Debugf("Connected to seccomp socket: pid=%v", ucred.pid)

				unixFile, err := c.(*net.UnixConn).File()
				if err != nil {
					return
				}

				for {
					siov := NewSeccompIovec(ucred)
					bytes, err := siov.ReceiveSeccompIovec(int(unixFile.Fd()))
					if err != nil {
						logger.Debugf("Disconnected from seccomp socket after failed receive: pid=%v, err=%s", ucred.pid, err)
						c.Close()
						return
					}

					if siov.IsValidSeccompIovec(bytes) {
						go s.Handler(int(unixFile.Fd()), siov)
					} else {
						go s.InvalidHandler(int(unixFile.Fd()), siov)
					}
				}
			}()
		}
	}()

	return &s, nil
}

func CallForkmknod(c container, dev config.Device, requestPID int) int {
	rootLink := fmt.Sprintf("/proc/%d/root", requestPID)
	rootPath, err := os.Readlink(rootLink)
	if err != nil {
		return int(-C.EPERM)
	}

	err, uid, gid, fsuid, fsgid := instance.TaskIDs(requestPID)
	if err != nil {
		return int(-C.EPERM)
	}

	if !path.IsAbs(dev["path"]) {
		cwdLink := fmt.Sprintf("/proc/%d/cwd", requestPID)
		prefixPath, err := os.Readlink(cwdLink)
		if err != nil {
			return int(-C.EPERM)
		}

		prefixPath = strings.TrimPrefix(prefixPath, rootPath)
		dev["hostpath"] = filepath.Join(c.RootfsPath(), rootPath, prefixPath, dev["path"])
	} else {
		dev["hostpath"] = filepath.Join(c.RootfsPath(), rootPath, dev["path"])
	}

	_, stderr, err := shared.RunCommandSplit(nil, util.GetExecPath(),
		"forksyscall", "mknod", dev["pid"], dev["path"],
		dev["mode_t"], dev["dev_t"], dev["hostpath"],
		fmt.Sprintf("%d", uid), fmt.Sprintf("%d", gid),
		fmt.Sprintf("%d", fsuid), fmt.Sprintf("%d", fsgid))
	if err != nil {
		errno, err := strconv.Atoi(stderr)
		if err != nil || errno == C.ENOANO {
			return int(-C.EPERM)
		}

		return -errno
	}

	return 0
}

// InvalidHandler sends a dummy message to LXC. LXC will notice the short write
// and send a default message to the kernel thereby avoiding a 30s hang.
func (s *SeccompServer) InvalidHandler(fd int, siov *SeccompIovec) {
	msghdr := C.struct_msghdr{}
	C.sendmsg(C.int(fd), &msghdr, C.MSG_NOSIGNAL)
	siov.PutSeccompIovec()
}

type MknodArgs struct {
	cMode C.mode_t
	cDev  C.dev_t
	cPid  C.pid_t
	path  string
}

func (s *SeccompServer) doDeviceSyscall(c container, args *MknodArgs, siov *SeccompIovec) int {
	dev := config.Device{}
	dev["type"] = "unix-char"
	dev["mode"] = fmt.Sprintf("%#o", args.cMode)
	dev["major"] = fmt.Sprintf("%d", unix.Major(uint64(args.cDev)))
	dev["minor"] = fmt.Sprintf("%d", unix.Minor(uint64(args.cDev)))
	dev["pid"] = fmt.Sprintf("%d", args.cPid)
	dev["path"] = args.path
	dev["mode_t"] = fmt.Sprintf("%d", args.cMode)
	dev["dev_t"] = fmt.Sprintf("%d", args.cDev)

	errno := CallForkmknod(c, dev, int(args.cPid))
	if errno != int(-C.ENOMEDIUM) {
		return errno
	}

	err := c.InsertSeccompUnixDevice(fmt.Sprintf("forkmknod.unix.%d", int(args.cPid)), dev, int(args.cPid))
	if err != nil {
		return int(-C.EPERM)
	}

	return 0
}

func (s *SeccompServer) HandleMknodSyscall(c container, siov *SeccompIovec) int {
	logger.Debug("Handling mknod syscall",
		log.Ctx{"container": c.Name(),
			"project":              c.Project(),
			"syscall_number":       siov.req.data.nr,
			"audit_architecture":   siov.req.data.arch,
			"seccomp_notify_id":    siov.req.id,
			"seccomp_notify_flags": siov.req.flags,
		})

	siov.resp.error = C.device_allowed(C.dev_t(siov.req.data.args[2]), C.mode_t(siov.req.data.args[1]))
	if siov.resp.error != 0 {
		logger.Debugf("Device not allowed")
		return int(siov.resp.error)
	}

	cPathBuf := [unix.PathMax]C.char{}
	_, err := C.pread(C.int(siov.memFd), unsafe.Pointer(&cPathBuf[0]), C.size_t(unix.PathMax), C.off_t(siov.req.data.args[0]))
	if err != nil {
		logger.Errorf("Failed to read memory for mknod syscall: %s", err)
		return int(-C.EPERM)
	}

	args := MknodArgs{
		cMode: C.mode_t(siov.req.data.args[1]),
		cDev:  C.dev_t(siov.req.data.args[2]),
		cPid:  C.pid_t(siov.req.pid),
		path:  C.GoString(&cPathBuf[0]),
	}

	return s.doDeviceSyscall(c, &args, siov)
}

func (s *SeccompServer) HandleMknodatSyscall(c container, siov *SeccompIovec) int {
	logger.Debug("Handling mknodat syscall",
		log.Ctx{"container": c.Name(),
			"project":              c.Project(),
			"syscall_number":       siov.req.data.nr,
			"audit_architecture":   siov.req.data.arch,
			"seccomp_notify_id":    siov.req.id,
			"seccomp_notify_flags": siov.req.flags,
		})

	// Make sure to handle 64bit kernel, 32bit container/userspace, LXD
	// built on 64bit userspace correctly.
	if int32(siov.req.data.args[0]) != int32(C.AT_FDCWD) {
		logger.Debugf("Non AT_FDCWD mknodat calls are not allowed")
		return int(-C.EINVAL)
	}

	siov.resp.error = C.device_allowed(C.dev_t(siov.req.data.args[3]), C.mode_t(siov.req.data.args[2]))
	if siov.resp.error != 0 {
		logger.Debugf("Device not allowed")
		return int(siov.resp.error)
	}

	cPathBuf := [unix.PathMax]C.char{}
	_, err := C.pread(C.int(siov.memFd), unsafe.Pointer(&cPathBuf[0]), C.size_t(unix.PathMax), C.off_t(siov.req.data.args[1]))
	if err != nil {
		logger.Errorf("Failed to read memory for mknodat syscall: %s", err)
		return int(-C.EPERM)
	}

	args := MknodArgs{
		cMode: C.mode_t(siov.req.data.args[2]),
		cDev:  C.dev_t(siov.req.data.args[3]),
		cPid:  C.pid_t(siov.req.pid),
		path:  C.GoString(&cPathBuf[0]),
	}

	return s.doDeviceSyscall(c, &args, siov)
}

type SetxattrArgs struct {
	nsuid   int64
	nsgid   int64
	nsfsuid int64
	nsfsgid int64
	size    int
	pid     int
	path    string
	name    string
	value   []byte
	flags   C.int
}

func (s *SeccompServer) HandleSetxattrSyscall(c container, siov *SeccompIovec) int {
	logger.Debug("Handling setxattr syscall",
		log.Ctx{"container": c.Name(),
			"project":              c.Project(),
			"syscall_number":       siov.req.data.nr,
			"audit_architecture":   siov.req.data.arch,
			"seccomp_notify_id":    siov.req.id,
			"seccomp_notify_flags": siov.req.flags,
		})

	args := SetxattrArgs{}

	args.pid = int(siov.req.pid)
	err, uid, gid, fsuid, fsgid := instance.TaskIDs(args.pid)
	if err != nil {
		return int(-C.EPERM)
	}

	idmapset, err := c.CurrentIdmap()
	if err != nil {
		return int(-C.EINVAL)
	}

	args.nsuid, args.nsgid = idmapset.ShiftFromNs(uid, gid)
	args.nsfsuid, args.nsfsgid = idmapset.ShiftFromNs(fsuid, fsgid)

	// const char *path
	cBuf := [unix.PathMax]C.char{}
	_, err = C.pread(C.int(siov.memFd), unsafe.Pointer(&cBuf[0]), C.size_t(unix.PathMax), C.off_t(siov.req.data.args[0]))
	if err != nil {
		logger.Errorf("Failed to read memory for setxattr syscall: %s", err)
		return int(-C.EPERM)
	}
	args.path = C.GoString(&cBuf[0])

	// const char *name
	_, err = C.pread(C.int(siov.memFd), unsafe.Pointer(&cBuf[0]), C.size_t(unix.PathMax), C.off_t(siov.req.data.args[1]))
	if err != nil {
		logger.Errorf("Failed to read memory for setxattr syscall: %s", err)
		return int(-C.EPERM)
	}
	args.name = C.GoString(&cBuf[0])

	// size_t size
	args.size = int(siov.req.data.args[3])

	// int flags
	args.flags = C.int(siov.req.data.args[4])

	buf := make([]byte, args.size)
	_, err = C.pread(C.int(siov.memFd), unsafe.Pointer(&buf[0]), C.size_t(args.size), C.off_t(siov.req.data.args[2]))
	if err != nil {
		logger.Errorf("Failed to read memory for setxattr syscall: %s", err)
		return int(-C.EPERM)
	}
	args.value = buf

	whiteout := 0
	if string(args.name) == "trusted.overlay.opaque" && string(args.value) == "y" {
		whiteout = 1
	}

	_, stderr, err := shared.RunCommandSplit(nil, util.GetExecPath(),
		"forksyscall",
		"setxattr",
		fmt.Sprintf("%d", args.pid),
		fmt.Sprintf("%d", args.nsuid),
		fmt.Sprintf("%d", args.nsgid),
		fmt.Sprintf("%d", args.nsfsuid),
		fmt.Sprintf("%d", args.nsfsgid),
		args.name,
		args.path,
		fmt.Sprintf("%d", args.flags),
		fmt.Sprintf("%d", whiteout),
		fmt.Sprintf("%d", args.size),
		fmt.Sprintf("%s", args.value))
	if err != nil {
		errno, err := strconv.Atoi(stderr)
		if err != nil || errno == C.ENOANO {
			return int(-C.EPERM)
		}

		return -errno
	}

	return 0
}

func (s *SeccompServer) HandleSyscall(c container, siov *SeccompIovec) int {
	switch int(C.seccomp_notify_get_syscall(siov.req, siov.resp)) {
	case LxdSeccompNotifyMknod:
		return s.HandleMknodSyscall(c, siov)
	case LxdSeccompNotifyMknodat:
		return s.HandleMknodatSyscall(c, siov)
	case LxdSeccompNotifySetxattr:
		return s.HandleSetxattrSyscall(c, siov)
	}

	return int(-C.EINVAL)
}

func (s *SeccompServer) Handler(fd int, siov *SeccompIovec) error {
	defer siov.PutSeccompIovec()

	c, err := findContainerForPid(int32(siov.msg.monitor_pid), s.d)
	if err != nil {
		siov.SendSeccompIovec(fd, int(-C.EPERM))
		logger.Errorf("Failed to find container for monitor %d", siov.msg.monitor_pid)
		return err
	}

	errno := s.HandleSyscall(c, siov)

	err = siov.SendSeccompIovec(fd, errno)
	if err != nil {
		return err
	}

	return nil
}

func (s *SeccompServer) Stop() error {
	os.Remove(s.path)
	return s.l.Close()
}
