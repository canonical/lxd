// +build linux
// +build cgo

package seccomp

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
	lxc "gopkg.in/lxc/go-lxc.v2"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/ucred"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/idmap"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/netutils"
	"github.com/lxc/lxd/shared/osarch"
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

#include "../include/lxd_seccomp.h"

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
					   int new_neg_errno, uint32_t flags)
{
	resp->error = new_neg_errno;
	resp->flags |= flags;
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
*/
import "C"

const lxdSeccompNotifyMknod = C.LXD_SECCOMP_NOTIFY_MKNOD
const lxdSeccompNotifyMknodat = C.LXD_SECCOMP_NOTIFY_MKNODAT
const lxdSeccompNotifySetxattr = C.LXD_SECCOMP_NOTIFY_SETXATTR

const seccompHeader = `2
`

const defaultSeccompPolicy = `reject_force_umount  # comment this to allow umount -f;  not recommended
[all]
kexec_load errno 38
open_by_handle_at errno 38
init_module errno 38
finit_module errno 38
delete_module errno 38
`

//          8 == SECCOMP_FILTER_FLAG_NEW_LISTENER
// 2146435072 == SECCOMP_RET_TRACE
const seccompNotifyDisallow = `seccomp errno 22 [1,2146435072,SCMP_CMP_MASKED_EQ,2146435072]
seccomp errno 22 [1,8,SCMP_CMP_MASKED_EQ,8]
`

const seccompNotifyMknod = `mknod notify [1,8192,SCMP_CMP_MASKED_EQ,61440]
mknod notify [1,24576,SCMP_CMP_MASKED_EQ,61440]
mknodat notify [2,8192,SCMP_CMP_MASKED_EQ,61440]
mknodat notify [2,24576,SCMP_CMP_MASKED_EQ,61440]
`
const seccompNotifySetxattr = `setxattr notify [3,1,SCMP_CMP_EQ]
`

const compatBlockingPolicy = `[%s]
compat_sys_rt_sigaction errno 38
stub_x32_rt_sigreturn errno 38
compat_sys_ioctl errno 38
compat_sys_readv errno 38
compat_sys_writev errno 38
compat_sys_recvfrom errno 38
compat_sys_sendmsg errno 38
compat_sys_recvmsg errno 38
stub_x32_execve errno 38
compat_sys_ptrace errno 38
compat_sys_rt_sigpending errno 38
compat_sys_rt_sigtimedwait errno 38
compat_sys_rt_sigqueueinfo errno 38
compat_sys_sigaltstack errno 38
compat_sys_timer_create errno 38
compat_sys_mq_notify errno 38
compat_sys_kexec_load errno 38
compat_sys_waitid errno 38
compat_sys_set_robust_list errno 38
compat_sys_get_robust_list errno 38
compat_sys_vmsplice errno 38
compat_sys_move_pages errno 38
compat_sys_preadv64 errno 38
compat_sys_pwritev64 errno 38
compat_sys_rt_tgsigqueueinfo errno 38
compat_sys_recvmmsg errno 38
compat_sys_sendmmsg errno 38
compat_sys_process_vm_readv errno 38
compat_sys_process_vm_writev errno 38
compat_sys_setsockopt errno 38
compat_sys_getsockopt errno 38
compat_sys_io_setup errno 38
compat_sys_io_submit errno 38
stub_x32_execveat errno 38
`

// Instance is a seccomp specific instance interface.
type Instance interface {
	Name() string
	Project() string
	ExpandedConfig() map[string]string
	IsPrivileged() bool
	DaemonState() *state.State
	Architecture() int
	RootfsPath() string
	CurrentIdmap() (*idmap.IdmapSet, error)
	InsertSeccompUnixDevice(prefix string, m deviceConfig.Device, pid int) error
}

var seccompPath = shared.VarPath("security", "seccomp")

// ProfilePath returns the seccomp path for the instance.
func ProfilePath(c Instance) string {
	return path.Join(seccompPath, c.Name())
}

// InstanceNeedsPolicy returns whether the instance needs a policy or not.
func InstanceNeedsPolicy(c Instance) bool {
	config := c.ExpandedConfig()

	// Check for text keys
	keys := []string{
		"raw.seccomp",
		"security.syscalls.whitelist",
		"security.syscalls.blacklist",
	}

	for _, k := range keys {
		_, hasKey := config[k]
		if hasKey {
			return true
		}
	}

	// Check for boolean keys that default to false
	keys = []string{
		"security.syscalls.blacklist_compat",
		"security.syscalls.intercept.mknod",
		"security.syscalls.intercept.setxattr",
	}

	for _, k := range keys {
		if shared.IsTrue(config[k]) {
			return true
		}
	}

	// Check for boolean keys that default to true
	keys = []string{
		"security.syscalls.blacklist_default",
	}

	for _, k := range keys {
		value, ok := config[k]
		if !ok || shared.IsTrue(value) {
			return true
		}
	}

	return false
}

// InstanceNeedsIntercept returns whether instance needs intercept.
func InstanceNeedsIntercept(c Instance) (bool, error) {
	// No need if privileged
	if c.IsPrivileged() {
		return false, nil
	}

	// If nested, assume the host handles it
	if c.DaemonState().OS.RunningInUserNS {
		return false, nil
	}

	config := c.ExpandedConfig()

	keys := []string{
		"security.syscalls.intercept.mknod",
		"security.syscalls.intercept.setxattr",
	}

	needed := false
	for _, k := range keys {
		if shared.IsTrue(config[k]) {
			needed = true
			break
		}
	}

	if needed {
		if !lxcSupportSeccompNotify(c.DaemonState()) {
			return needed, fmt.Errorf("System doesn't support syscall interception")
		}
	}

	return needed, nil
}

func seccompGetPolicyContent(c Instance) (string, error) {
	config := c.ExpandedConfig()

	// Full policy override
	raw := config["raw.seccomp"]
	if raw != "" {
		return raw, nil
	}

	// Policy header
	policy := seccompHeader
	whitelist := config["security.syscalls.whitelist"]
	if whitelist != "" {
		policy += "whitelist\n[all]\n"
		policy += whitelist
	} else {
		policy += "blacklist\n"

		defaultFlag, ok := config["security.syscalls.blacklist_default"]
		if !ok || shared.IsTrue(defaultFlag) {
			policy += defaultSeccompPolicy
		}
	}

	// Syscall interception
	ok, err := InstanceNeedsIntercept(c)
	if err != nil {
		return "", err
	}

	if ok {
		if shared.IsTrue(config["security.syscalls.intercept.mknod"]) {
			policy += seccompNotifyMknod
		}

		if shared.IsTrue(config["security.syscalls.intercept.setxattr"]) {
			policy += seccompNotifySetxattr
		}

		// Prevent the container from overriding our syscall
		// supervision.
		policy += seccompNotifyDisallow
	}

	if whitelist != "" {
		return policy, nil
	}

	// Additional blacklist entries
	compat := config["security.syscalls.blacklist_compat"]
	if shared.IsTrue(compat) {
		arch, err := osarch.ArchitectureName(c.Architecture())
		if err != nil {
			return "", err
		}
		policy += fmt.Sprintf(compatBlockingPolicy, arch)
	}

	blacklist := config["security.syscalls.blacklist"]
	if blacklist != "" {
		policy += blacklist
	}

	return policy, nil
}

// CreateProfile creates a seccomp profile.
func CreateProfile(c Instance) error {
	/* Unlike apparmor, there is no way to "cache" profiles, and profiles
	 * are automatically unloaded when a task dies. Thus, we don't need to
	 * unload them when a container stops, and we don't have to worry about
	 * the mtime on the file for any compiler purpose, so let's just write
	 * out the profile.
	 */
	if !InstanceNeedsPolicy(c) {
		return nil
	}

	profile, err := seccompGetPolicyContent(c)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(seccompPath, 0700); err != nil {
		return err
	}

	return ioutil.WriteFile(ProfilePath(c), []byte(profile), 0600)
}

// DeleteProfile removes a seccomp profile.
func DeleteProfile(c Instance) {
	/* similar to AppArmor, if we've never started this container, the
	 * delete can fail and that's ok.
	 */
	os.Remove(ProfilePath(c))
}

// Server defines a seccomp server.
type Server struct {
	s    *state.State
	path string
	l    net.Listener
}

// Iovec defines an iovec to move data between kernel and userspace.
type Iovec struct {
	ucred  *ucred.UCred
	memFd  int
	procFd int
	msg    *C.struct_seccomp_notify_proxy_msg
	req    *C.struct_seccomp_notif
	resp   *C.struct_seccomp_notif_resp
	cookie *C.char
	iov    *C.struct_iovec
}

// NewSeccompIovec creates a new seccomp iovec.
func NewSeccompIovec(ucred *ucred.UCred) *Iovec {
	msgPtr := C.malloc(C.sizeof_struct_seccomp_notify_proxy_msg)
	msg := (*C.struct_seccomp_notify_proxy_msg)(msgPtr)
	C.memset(msgPtr, 0, C.sizeof_struct_seccomp_notify_proxy_msg)

	regPtr := C.malloc(C.sizeof_struct_seccomp_notif)
	req := (*C.struct_seccomp_notif)(regPtr)
	C.memset(regPtr, 0, C.sizeof_struct_seccomp_notif)

	respPtr := C.malloc(C.sizeof_struct_seccomp_notif_resp)
	resp := (*C.struct_seccomp_notif_resp)(respPtr)
	C.memset(respPtr, 0, C.sizeof_struct_seccomp_notif_resp)

	cookiePtr := C.malloc(64 * C.sizeof_char)
	cookie := (*C.char)(cookiePtr)
	C.memset(cookiePtr, 0, 64*C.sizeof_char)

	iovUnsafePtr := C.malloc(4 * C.sizeof_struct_iovec)
	iov := (*C.struct_iovec)(iovUnsafePtr)
	C.memset(iovUnsafePtr, 0, 4*C.sizeof_struct_iovec)

	C.prepare_seccomp_iovec(iov, msg, req, resp, cookie)

	return &Iovec{
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

// PutSeccompIovec puts a seccomp iovec.
func (siov *Iovec) PutSeccompIovec() {
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

// ReceiveSeccompIovec receives a seccomp iovec.
func (siov *Iovec) ReceiveSeccompIovec(fd int) (uint64, error) {
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

// IsValidSeccompIovec checks whether a seccomp iovec is valid.
func (siov *Iovec) IsValidSeccompIovec(size uint64) bool {
	if size < uint64(C.SECCOMP_MSG_SIZE_MIN) {
		logger.Warnf("Disconnected from seccomp socket after incomplete receive")
		return false
	}
	if siov.msg.__reserved != 0 {
		logger.Warnf("Disconnected from seccomp socket after client sent non-zero reserved field: pid=%v",
			siov.ucred.PID)
		return false
	}

	if siov.msg.sizes.seccomp_notif != C.expected_sizes.seccomp_notif {
		logger.Warnf("Disconnected from seccomp socket since client uses different seccomp_notif sizes: %d != %d, pid=%v",
			siov.msg.sizes.seccomp_notif, C.expected_sizes.seccomp_notif, siov.ucred.PID)
		return false
	}

	if siov.msg.sizes.seccomp_notif_resp != C.expected_sizes.seccomp_notif_resp {
		logger.Warnf("Disconnected from seccomp socket since client uses different seccomp_notif_resp sizes: %d != %d, pid=%v",
			siov.msg.sizes.seccomp_notif_resp, C.expected_sizes.seccomp_notif_resp, siov.ucred.PID)
		return false
	}

	if siov.msg.sizes.seccomp_data != C.expected_sizes.seccomp_data {
		logger.Warnf("Disconnected from seccomp socket since client uses different seccomp_data sizes: %d != %d, pid=%v",
			siov.msg.sizes.seccomp_data, C.expected_sizes.seccomp_data, siov.ucred.PID)
		return false
	}

	return true
}

// SendSeccompIovec sends seccomp iovec.
func (siov *Iovec) SendSeccompIovec(fd int, errno int, flags uint32) error {
	C.seccomp_notify_update_response(siov.resp, C.int(errno), C.uint32_t(flags))

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
		logger.Debugf("Disconnected from seccomp socket after failed write for process %v: %s", siov.ucred.PID, err)
		return fmt.Errorf("Failed to send response to seccomp client %v", siov.ucred.PID)
	}

	if uint64(bytes) != uint64(C.SECCOMP_MSG_SIZE_MIN) {
		logger.Debugf("Disconnected from seccomp socket after short write: pid=%v", siov.ucred.PID)
		return fmt.Errorf("Failed to send full response to seccomp client %v", siov.ucred.PID)
	}

	return nil
}

// NewSeccompServer creates a new seccomp server.
func NewSeccompServer(s *state.State, path string, findPID func(pid int32, state *state.State) (Instance, error)) (*Server, error) {
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
	server := Server{
		s:    s,
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
				ucred, err := ucred.GetCred(c.(*net.UnixConn))
				if err != nil {
					logger.Errorf("Unable to get ucred from seccomp socket client: %v", err)
					return
				}

				logger.Debugf("Connected to seccomp socket: pid=%v", ucred.PID)

				unixFile, err := c.(*net.UnixConn).File()
				if err != nil {
					return
				}

				for {
					siov := NewSeccompIovec(ucred)
					bytes, err := siov.ReceiveSeccompIovec(int(unixFile.Fd()))
					if err != nil {
						logger.Debugf("Disconnected from seccomp socket after failed receive: pid=%v, err=%s", ucred.PID, err)
						c.Close()
						return
					}

					if siov.IsValidSeccompIovec(bytes) {
						go server.handler(int(unixFile.Fd()), siov, findPID)
					} else {
						go server.InvalidHandler(int(unixFile.Fd()), siov)
					}
				}
			}()
		}
	}()

	return &server, nil
}

// TaskIDs returns the task IDs for a process.
func TaskIDs(pid int) (int64, int64, int64, int64, error) {
	status, err := ioutil.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return -1, -1, -1, -1, err
	}

	reUID := regexp.MustCompile("Uid:\\s*([0-9]*)\\s*([0-9]*)\\s*([0-9]*)\\s*([0-9]*)")
	reGID := regexp.MustCompile("Gid:\\s*([0-9]*)\\s*([0-9]*)\\s*([0-9]*)\\s*([0-9]*)")
	var UID int64 = -1
	var GID int64 = -1
	var fsUID int64 = -1
	var fsGID int64 = -1
	UIDFound := false
	GIDFound := false
	for _, line := range strings.Split(string(status), "\n") {
		if UIDFound && GIDFound {
			break
		}

		if !UIDFound {
			m := reUID.FindStringSubmatch(line)
			if m != nil && len(m) > 2 {
				// effective uid
				result, err := strconv.ParseInt(m[2], 10, 64)
				if err != nil {
					return -1, -1, -1, -1, err
				}

				UID = result
				UIDFound = true
			}

			if m != nil && len(m) > 4 {
				// fsuid
				result, err := strconv.ParseInt(m[4], 10, 64)
				if err != nil {
					return -1, -1, -1, -1, err
				}

				fsUID = result
			}

			continue
		}

		if !GIDFound {
			m := reGID.FindStringSubmatch(line)
			if m != nil && len(m) > 2 {
				// effective gid
				result, err := strconv.ParseInt(m[2], 10, 64)
				if err != nil {
					return -1, -1, -1, -1, err
				}

				GID = result
				GIDFound = true
			}

			if m != nil && len(m) > 4 {
				// fsgid
				result, err := strconv.ParseInt(m[4], 10, 64)
				if err != nil {
					return -1, -1, -1, -1, err
				}

				fsGID = result
			}

			continue
		}
	}

	return UID, GID, fsUID, fsGID, nil
}

// CallForkmknod executes fork mknod.
func CallForkmknod(c Instance, dev deviceConfig.Device, requestPID int) int {
	uid, gid, fsuid, fsgid, err := TaskIDs(requestPID)
	if err != nil {
		return int(-C.EPERM)
	}

	_, stderr, err := shared.RunCommandSplit(nil, util.GetExecPath(),
		"forksyscall", "mknod", dev["pid"], dev["path"],
		dev["mode_t"], dev["dev_t"],
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
func (s *Server) InvalidHandler(fd int, siov *Iovec) {
	msghdr := C.struct_msghdr{}
	C.sendmsg(C.int(fd), &msghdr, C.MSG_NOSIGNAL)
	siov.PutSeccompIovec()
}

// MknodArgs arguments for mknod.
type MknodArgs struct {
	cMode C.mode_t
	cDev  C.dev_t
	cPid  C.pid_t
	path  string
}

func (s *Server) doDeviceSyscall(c Instance, args *MknodArgs, siov *Iovec) int {
	dev := deviceConfig.Device{}
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

// HandleMknodSyscall handles a mknod syscall.
func (s *Server) HandleMknodSyscall(c Instance, siov *Iovec) int {
	ctx := log.Ctx{"container": c.Name(),
		"project":              c.Project(),
		"syscall_number":       siov.req.data.nr,
		"audit_architecture":   siov.req.data.arch,
		"seccomp_notify_id":    siov.req.id,
		"seccomp_notify_flags": siov.req.flags,
	}

	defer logger.Debug("Handling mknod syscall", ctx)

	if C.device_allowed(C.dev_t(siov.req.data.args[2]), C.mode_t(siov.req.data.args[1])) < 0 {
		ctx["err"] = "Device not allowed"
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(siov.resp.error)
	}

	cPathBuf := [unix.PathMax]C.char{}
	_, err := C.pread(C.int(siov.memFd), unsafe.Pointer(&cPathBuf[0]), C.size_t(unix.PathMax), C.off_t(siov.req.data.args[0]))
	if err != nil {
		ctx["err"] = fmt.Sprintf("Failed to read memory for mknod syscall: %s", err)
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(-C.EPERM)
	}

	args := MknodArgs{
		cMode: C.mode_t(siov.req.data.args[1]),
		cDev:  C.dev_t(siov.req.data.args[2]),
		cPid:  C.pid_t(siov.req.pid),
		path:  C.GoString(&cPathBuf[0]),
	}
	ctx["syscall_args"] = &args

	return s.doDeviceSyscall(c, &args, siov)
}

// HandleMknodatSyscall handles a mknodat syscall.
func (s *Server) HandleMknodatSyscall(c Instance, siov *Iovec) int {
	ctx := log.Ctx{"container": c.Name(),
		"project":              c.Project(),
		"syscall_number":       siov.req.data.nr,
		"audit_architecture":   siov.req.data.arch,
		"seccomp_notify_id":    siov.req.id,
		"seccomp_notify_flags": siov.req.flags,
	}

	defer logger.Debug("Handling mknodat syscall", ctx)

	// Make sure to handle 64bit kernel, 32bit container/userspace, LXD
	// built on 64bit userspace correctly.
	if int32(siov.req.data.args[0]) != int32(C.AT_FDCWD) {
		ctx["err"] = "Non AT_FDCWD mknodat calls are not allowed"
		logger.Debug("bla", ctx)
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(-C.EINVAL)
	}

	siov.resp.error = C.device_allowed(C.dev_t(siov.req.data.args[3]), C.mode_t(siov.req.data.args[2]))
	if siov.resp.error != 0 {
		ctx["err"] = "Device not allowed"
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(siov.resp.error)
	}

	cPathBuf := [unix.PathMax]C.char{}
	_, err := C.pread(C.int(siov.memFd), unsafe.Pointer(&cPathBuf[0]), C.size_t(unix.PathMax), C.off_t(siov.req.data.args[1]))
	if err != nil {
		ctx["err"] = "Failed to read memory for mknodat syscall: %s"
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(-C.EPERM)
	}

	args := MknodArgs{
		cMode: C.mode_t(siov.req.data.args[2]),
		cDev:  C.dev_t(siov.req.data.args[3]),
		cPid:  C.pid_t(siov.req.pid),
		path:  C.GoString(&cPathBuf[0]),
	}
	ctx["syscall_args"] = &args

	return s.doDeviceSyscall(c, &args, siov)
}

// SetxattrArgs arguments for setxattr.
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

// HandleSetxattrSyscall handles setxattr syscalls.
func (s *Server) HandleSetxattrSyscall(c Instance, siov *Iovec) int {
	ctx := log.Ctx{"container": c.Name(),
		"project":              c.Project(),
		"syscall_number":       siov.req.data.nr,
		"audit_architecture":   siov.req.data.arch,
		"seccomp_notify_id":    siov.req.id,
		"seccomp_notify_flags": siov.req.flags,
	}

	defer logger.Debug("Handling setxattr syscall", ctx)

	args := SetxattrArgs{}

	args.pid = int(siov.req.pid)
	uid, gid, fsuid, fsgid, err := TaskIDs(args.pid)
	if err != nil {
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(-C.EPERM)
	}

	idmapset, err := c.CurrentIdmap()
	if err != nil {
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(-C.EINVAL)
	}

	args.nsuid, args.nsgid = idmapset.ShiftFromNs(uid, gid)
	args.nsfsuid, args.nsfsgid = idmapset.ShiftFromNs(fsuid, fsgid)

	// const char *path
	cBuf := [unix.PathMax]C.char{}
	_, err = C.pread(C.int(siov.memFd), unsafe.Pointer(&cBuf[0]), C.size_t(unix.PathMax), C.off_t(siov.req.data.args[0]))
	if err != nil {
		ctx["err"] = fmt.Sprintf("Failed to read memory for setxattr syscall: %s", err)
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(-C.EPERM)
	}
	args.path = C.GoString(&cBuf[0])

	// const char *name
	_, err = C.pread(C.int(siov.memFd), unsafe.Pointer(&cBuf[0]), C.size_t(unix.PathMax), C.off_t(siov.req.data.args[1]))
	if err != nil {
		ctx["err"] = fmt.Sprintf("Failed to read memory for setxattr syscall: %s", err)
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

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
		ctx["err"] = fmt.Sprintf("Failed to read memory for setxattr syscall: %s", err)
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(-C.EPERM)
	}
	args.value = buf

	whiteout := 0
	if string(args.name) == "trusted.overlay.opaque" && string(args.value) == "y" {
		whiteout = 1
	} else if s.s.OS.SeccompListenerContinue {
		ctx["syscall_continue"] = "true"
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
		return 0
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

func (s *Server) handleSyscall(c Instance, siov *Iovec) int {
	switch int(C.seccomp_notify_get_syscall(siov.req, siov.resp)) {
	case lxdSeccompNotifyMknod:
		return s.HandleMknodSyscall(c, siov)
	case lxdSeccompNotifyMknodat:
		return s.HandleMknodatSyscall(c, siov)
	case lxdSeccompNotifySetxattr:
		return s.HandleSetxattrSyscall(c, siov)
	}

	return int(-C.EINVAL)
}

const seccompUserNotifFlagContinue uint32 = 0x00000001

func (s *Server) handler(fd int, siov *Iovec, findPID func(pid int32, state *state.State) (Instance, error)) error {
	defer siov.PutSeccompIovec()

	c, err := findPID(int32(siov.msg.monitor_pid), s.s)
	if err != nil {
		if s.s.OS.SeccompListenerContinue {
			siov.SendSeccompIovec(fd, 0, seccompUserNotifFlagContinue)
		} else {
			siov.SendSeccompIovec(fd, int(-C.EPERM), 0)
		}
		logger.Errorf("Failed to find container for monitor %d", siov.msg.monitor_pid)
		return err
	}

	errno := s.handleSyscall(c, siov)

	err = siov.SendSeccompIovec(fd, errno, 0)
	if err != nil {
		return err
	}

	return nil
}

// Stop stops a seccomp server.
func (s *Server) Stop() error {
	os.Remove(s.path)
	return s.l.Close()
}

func lxcSupportSeccompNotify(state *state.State) bool {
	if !state.OS.SeccompListener {
		return false
	}

	if !state.OS.LXCFeatures["seccomp_notify"] {
		return false
	}

	c, err := lxc.NewContainer("test-seccomp", state.OS.LxcPath)
	if err != nil {
		return false
	}

	err = c.SetConfigItem("lxc.seccomp.notify.proxy", fmt.Sprintf("unix:%s", shared.VarPath("seccomp.socket")))
	if err != nil {
		return false
	}

	c.Release()
	return true
}
