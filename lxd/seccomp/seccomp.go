//go:build linux && cgo

package seccomp

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
#include <sys/capability.h>
#include <sys/mount.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <sys/sysmacros.h>
#include <sys/types.h>
#include <unistd.h>

#include "../include/lxd_bpf.h"
#include "../include/lxd_seccomp.h"
#include "../include/memory_utils.h"
#include "../include/process_utils.h"
#include "../include/syscall_wrappers.h"

struct seccomp_notif_sizes expected_sizes;

struct seccomp_notify_proxy_msg {
	uint64_t __reserved;
	pid_t monitor_pid;
	pid_t init_pid;
	struct seccomp_notif_sizes sizes;
	uint64_t cookie_len;
	// followed by: seccomp_notif, seccomp_notif_resp, cookie
};

#define LXD_SCHED_PARAM_SIZE (sizeof(struct sched_param))
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
	switch (mode & S_IFMT) {
	case S_IFCHR:
		if (dev == makedev(0, 0)) // whiteout
			return 0;
		else if (dev == makedev(5, 1)) // /dev/console
			return 0;
		else if (dev == makedev(1, 7)) // /dev/full
			return 0;
		else if (dev == makedev(1, 3)) // /dev/null
			return 0;
		else if (dev == makedev(1, 8)) // /dev/random
			return 0;
		else if (dev == makedev(5, 0)) // /dev/tty
			return 0;
		else if (dev == makedev(1, 9)) // /dev/urandom
			return 0;
		else if (dev == makedev(1, 5)) // /dev/zero
			return 0;
	}

	return -EPERM;
}

#include <linux/audit.h>

struct lxd_seccomp_data_arch {
	int arch;
	int nr_mknod;
	int nr_mknodat;
	int nr_setxattr;
	int nr_mount;
	int nr_bpf;
	int nr_sched_setscheduler;
	int nr_sysinfo;
	int nr_finit_module;
};

#define LXD_SECCOMP_NOTIFY_MKNOD    0
#define LXD_SECCOMP_NOTIFY_MKNODAT  1
#define LXD_SECCOMP_NOTIFY_SETXATTR 2
#define LXD_SECCOMP_NOTIFY_MOUNT 3
#define LXD_SECCOMP_NOTIFY_BPF 4
#define LXD_SECCOMP_NOTIFY_SCHED_SETSCHEDULER 5
#define LXD_SECCOMP_NOTIFY_SYSINFO 6
#define LXD_SECCOMP_NOTIFY_FINIT_MODULE 7

// ordered by likelihood of usage...
static const struct lxd_seccomp_data_arch seccomp_notify_syscall_table[] = {
	{ -1, LXD_SECCOMP_NOTIFY_MKNOD, LXD_SECCOMP_NOTIFY_MKNODAT, LXD_SECCOMP_NOTIFY_SETXATTR, LXD_SECCOMP_NOTIFY_MOUNT, LXD_SECCOMP_NOTIFY_BPF, LXD_SECCOMP_NOTIFY_SCHED_SETSCHEDULER, LXD_SECCOMP_NOTIFY_SYSINFO, LXD_SECCOMP_NOTIFY_FINIT_MODULE },
#ifdef AUDIT_ARCH_X86_64
	{ AUDIT_ARCH_X86_64,       133, 259, 188, 165, 321, 144, 99, 313 },
#endif
#ifdef AUDIT_ARCH_I386
	{ AUDIT_ARCH_I386,         14, 297, 226,  21, 357, 156, 116, 350 },
#endif
#ifdef AUDIT_ARCH_AARCH64
	{ AUDIT_ARCH_AARCH64,      -1,  33,   5,  21, 280, 156, 179, 273 },
#endif
#ifdef AUDIT_ARCH_ARM
	{ AUDIT_ARCH_ARM,          14, 324, 226,  21, 386, 156, 116, 379 },
#endif
#ifdef AUDIT_ARCH_ARMEB
	{ AUDIT_ARCH_ARMEB,        14, 324, 226,  21, 386, 156, 116, 379 },
#endif
#ifdef AUDIT_ARCH_S390
	{ AUDIT_ARCH_S390,         14, 290, 224,  21, 386, 156, 116, 344 },
#endif
#ifdef AUDIT_ARCH_S390X
	{ AUDIT_ARCH_S390X,        14, 290, 224,  21, 351, 156, 116, 344 },
#endif
#ifdef AUDIT_ARCH_PPC
	{ AUDIT_ARCH_PPC,          14, 288, 209,  21, 361, 156, 116, 353 },
#endif
#ifdef AUDIT_ARCH_PPC64
	{ AUDIT_ARCH_PPC64,        14, 288, 209,  21, 361, 156, 116, 353 },
#endif
#ifdef AUDIT_ARCH_PPC64LE
	{ AUDIT_ARCH_PPC64LE,      14, 288, 209,  21, 361, 156, 116, 353 },
#endif
#ifdef AUDIT_ARCH_RISCV64
	{ AUDIT_ARCH_RISCV64,      -1,  33,   5,  40, 280, -1, 179, 273 },
#endif
#ifdef AUDIT_ARCH_SPARC
	{ AUDIT_ARCH_SPARC,        14, 286, 169, 167, 349, 243, 214, 342 },
#endif
#ifdef AUDIT_ARCH_SPARC64
	{ AUDIT_ARCH_SPARC64,      14, 286, 169, 167, 349, 243, 214, 342 },
#endif
#ifdef AUDIT_ARCH_MIPS
	{ AUDIT_ARCH_MIPS,         14, 290, 224,  21,  -1, 141, 4116, 4348 },
#endif
#ifdef AUDIT_ARCH_MIPSEL
	{ AUDIT_ARCH_MIPSEL,       14, 290, 224,  21,  -1, 141, 4116, 4348 },
#endif
#ifdef AUDIT_ARCH_MIPS64
	{ AUDIT_ARCH_MIPS64,      131, 249, 180, 160,  -1, 141, 5097, 5307 },
#endif
#ifdef AUDIT_ARCH_MIPS64N32
	{ AUDIT_ARCH_MIPS64N32,   131, 253, 180, 160,  -1, 141, 4116, 6312 },
#endif
#ifdef AUDIT_ARCH_MIPSEL64
	{ AUDIT_ARCH_MIPSEL64,    131, 249, 180, 160,  -1, 141, 5097, 5307 },
#endif
#ifdef AUDIT_ARCH_MIPSEL64N32
	{ AUDIT_ARCH_MIPSEL64N32, 131, 253, 180, 160,  -1, 141, 4116, 6312 },
#endif
#ifdef AUDIT_ARCH_LOONGARCH64
	{ AUDIT_ARCH_LOONGARCH64, -1,  33,   5,  40, 280, 119, 179 },
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

		if (entry->nr_mount == req->data.nr)
			return LXD_SECCOMP_NOTIFY_MOUNT;

		if (entry->nr_bpf == req->data.nr)
			return LXD_SECCOMP_NOTIFY_BPF;

		if (entry->nr_sched_setscheduler == req->data.nr)
			return LXD_SECCOMP_NOTIFY_SCHED_SETSCHEDULER;

		if (entry->nr_sysinfo == req->data.nr)
			return LXD_SECCOMP_NOTIFY_SYSINFO;

		if (entry->nr_finit_module == req->data.nr)
			return LXD_SECCOMP_NOTIFY_FINIT_MODULE;

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

// We use the BPF_DEVCG_DEV_CHAR macro as a cheap way to detect whether the kernel has
// the correct headers available to be compiled for bpf support. Since cgo doesn't have
// a good way of letting us probe for structs or enums the alternative would be to vendor
// bpf.h similar to what we do for seccomp itself. But that's annoying since bpf.h is quite
// large. So users that want bpf interception support should make sure to have the relevant
// header available at build time.
static inline int pidfd_getfd(int pidfd, int fd, int flags)
{
	return syscall(__NR_pidfd_getfd, pidfd, fd, flags);
}

#define ptr_to_u64(p) ((__u64)((uintptr_t)(p)))

static inline int bpf(int cmd, union bpf_attr *attr, size_t size)
{
	return syscall(__NR_bpf, cmd, attr, size);
}

static int handle_bpf_syscall(pid_t pid_target, int notify_fd, int mem_fd,
			      int tgid, struct seccomp_notify_proxy_msg *msg,
			      struct seccomp_notif *req, struct seccomp_notif_resp *resp,
			      int *bpf_cmd, int *bpf_prog_type, int *bpf_attach_type)
{
	__do_close int pidfd = -EBADF, bpf_target_fd = -EBADF, bpf_attach_fd = -EBADF,
		       bpf_prog_fd = -EBADF;
	__do_free struct bpf_insn *insn = NULL;
	char log_buf[4096] = {};
	char license[128] = {};
	size_t insn_size = 0;
	union bpf_attr attr = {}, new_attr = {};
	unsigned int attr_len = sizeof(attr);
	struct seccomp_notif_addfd addfd = {};
	int ret;
	int cmd;

	*bpf_cmd		= -EINVAL;
	*bpf_prog_type		= -EINVAL;
	*bpf_attach_type	= -EINVAL;

	if (attr_len < req->data.args[2])
		return -EFBIG;
	attr_len = req->data.args[2];

	*bpf_cmd = req->data.args[0];
	switch (req->data.args[0]) {
	case BPF_PROG_LOAD:
		cmd = BPF_PROG_LOAD;
		break;
	case BPF_PROG_ATTACH:
		cmd = BPF_PROG_ATTACH;
		break;
	case BPF_PROG_DETACH:
		cmd = BPF_PROG_DETACH;
		break;
	default:
		return -EINVAL;
	}

	ret = pread(mem_fd, &attr, attr_len, req->data.args[1]);
	if (ret < 0)
		return -errno;

	*bpf_prog_type = attr.prog_type;

	pidfd = lxd_pidfd_open(tgid, 0);
	if (pidfd < 0)
		return -errno;

	if (ioctl(notify_fd, SECCOMP_IOCTL_NOTIF_ID_VALID, &req->id))
		return -errno;

	switch (cmd) {
	case BPF_PROG_LOAD:
		if (attr.prog_type != BPF_PROG_TYPE_CGROUP_DEVICE)
			return -EINVAL;

		// bpf is currently limited to 1 million instructions. Don't
		// allow the container to allocate more than that.
		if (attr.insn_cnt > 1000000)
			return -EINVAL;

		insn_size = sizeof(struct bpf_insn) * attr.insn_cnt;

		insn = malloc(insn_size);
		if (!insn)
			return -ENOMEM;

		ret = pread(mem_fd, insn, insn_size, attr.insns);
		if (ret < 0)
			return -errno;
		if (ret != insn_size)
			return -EIO;

		memcpy(&new_attr, &attr, sizeof(attr));

		if (attr.log_size > sizeof(log_buf))
			new_attr.log_size = sizeof(log_buf);

		if (new_attr.log_size > 0)
			new_attr.log_buf = ptr_to_u64(log_buf);

		if (attr.license && pread(mem_fd, license, sizeof(license), attr.license) < 0)
			return -errno;

		new_attr.insns		= ptr_to_u64(insn);
		new_attr.license	= ptr_to_u64(license);
		bpf_prog_fd = bpf(cmd, &new_attr, sizeof(new_attr));
		if (bpf_prog_fd < 0) {
			int saved_errno = errno;

			if ((new_attr.log_size) > 0 && (pwrite(mem_fd, log_buf, new_attr.log_size,
							       attr.log_buf) != new_attr.log_size))
				errno = saved_errno;
			return -errno;
		}

		addfd.srcfd	= bpf_prog_fd;
		addfd.id	= req->id;
		addfd.flags	= 0;
		ret = ioctl(notify_fd, SECCOMP_IOCTL_NOTIF_ADDFD, &addfd);
		if (ret < 0)
			return -errno;

		resp->val = ret;
		ret = 0;
		break;
	case BPF_PROG_ATTACH:
		if (attr.attach_type != BPF_CGROUP_DEVICE)
			return -EINVAL;

		*bpf_attach_type = attr.attach_type;

		bpf_target_fd = pidfd_getfd(pidfd, attr.target_fd, 0);
		if (bpf_target_fd < 0)
			return -errno;

		bpf_attach_fd = pidfd_getfd(pidfd, attr.attach_bpf_fd, 0);
		if (bpf_attach_fd < 0)
			return -errno;

		if (tgid != pid_target) {
			// Make sure that the file descriptor table is shared
			// so we can be sure that we're talking about the same
			// open files.
			if (!filetable_shared(tgid, pid_target))
				return -EINVAL;

			// Make sure that the threadgroup leader hasn't been
			// recycled in the meantime so we're not accidentally
			// looking at a different threadgroup with the same
			// open files.
			if (!process_still_alive(pidfd))
				return -EINVAL;
		}

		attr.target_fd		= bpf_target_fd;
		attr.attach_bpf_fd	= bpf_attach_fd;
		ret = bpf(cmd, &attr, attr_len);
		break;
	case BPF_PROG_DETACH:
		if (attr.attach_type != BPF_CGROUP_DEVICE)
			return -EINVAL;

		*bpf_attach_type = attr.attach_type;

		bpf_target_fd = pidfd_getfd(pidfd, attr.target_fd, 0);
		if (bpf_target_fd < 0)
			return -errno;

		bpf_attach_fd = pidfd_getfd(pidfd, attr.attach_bpf_fd, 0);
		if (bpf_attach_fd < 0)
			return -errno;

		if (tgid != pid_target) {
			// Make sure that the file descriptor table is shared
			// so we can be sure that we're talking about the same
			// open files.
			if (!filetable_shared(tgid, pid_target))
				return -EINVAL;

			// Make sure that the threadgroup leader hasn't been
			// recycled in the meantime so we're not accidentally
			// looking at a different threadgroup with the same
			// open files.
			if (!process_still_alive(pidfd))
				return -EINVAL;
		}

		attr.target_fd		= bpf_target_fd;
		attr.attach_bpf_fd	= bpf_attach_fd;
		ret = bpf(cmd, &attr, attr_len);
		break;
	}

	return ret;
}

#ifndef MS_LAZYTIME
#define MS_LAZYTIME (1<<25)
#endif

static bool lxd_pid_cap_is_set(pid_t pid, cap_value_t cap, cap_flag_t flag)
{
	int ret;
	cap_t caps;
	cap_flag_value_t flagval;

	caps = cap_get_pid(pid);
	if (!caps)
		return false;

	ret = cap_get_flag(caps, cap, flag, &flagval);
	if (ret < 0)
		return false;

	return flagval == CAP_SET;
}

*/
import "C"

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unsafe"

	liblxc "github.com/lxc/go-lxc"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/cgroup"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/idmap"
	_ "github.com/canonical/lxd/lxd/include" // Used by cgo
	"github.com/canonical/lxd/lxd/linux"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/subprocess"
	"github.com/canonical/lxd/lxd/ucred"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/netutils"
	"github.com/canonical/lxd/shared/osarch"
)

const lxdSeccompNotifyMknod = C.LXD_SECCOMP_NOTIFY_MKNOD
const lxdSeccompNotifyMknodat = C.LXD_SECCOMP_NOTIFY_MKNODAT
const lxdSeccompNotifySetxattr = C.LXD_SECCOMP_NOTIFY_SETXATTR
const lxdSeccompNotifyMount = C.LXD_SECCOMP_NOTIFY_MOUNT
const lxdSeccompNotifyBpf = C.LXD_SECCOMP_NOTIFY_BPF
const lxdSeccompNotifySchedSetscheduler = C.LXD_SECCOMP_NOTIFY_SCHED_SETSCHEDULER
const lxdSeccompNotifySysinfo = C.LXD_SECCOMP_NOTIFY_SYSINFO
const lxdSeccompNotifyFinitModule = C.LXD_SECCOMP_NOTIFY_FINIT_MODULE

const seccompHeader = `2
`

const defaultSeccompPolicy = `reject_force_umount  # comment this to allow umount -f;  not recommended
[all]
kexec_load errno 38
open_by_handle_at errno 38
init_module errno 38
delete_module errno 38
`

//	8 == SECCOMP_FILTER_FLAG_NEW_LISTENER
//
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

const seccompNotifySchedSetscheduler = `sched_setscheduler notify
`

const seccompNotifySysinfo = `sysinfo notify
`

const seccompNotifyModule = `finit_module notify
`

const seccompBlockNewMountAPI = `fsopen errno 38
fsconfig errno 38
fsinfo errno 38
fsmount errno 38
fspick errno 38
open_tree errno 38
move_mount errno 38
openat2 errno 38
`

// We don't want to filter any of the following flag combinations since they do
// not cause the creation of a new superblock:
//
// MS_REMOUNT
// MS_BIND
// MS_MOVE
// MS_UNBINDABLE
// MS_PRIVATE
// MS_SLAVE
// MS_SHARED
// MS_KERNMOUNT
// MS_I_VERSION
//
// So define the following mask of allowed flags:
//
// long unsigned int mask = MS_MGC_VAL | MS_RDONLY | MS_NOSUID | MS_NODEV |
//                          MS_NOEXEC | MS_SYNCHRONOUS | MS_MANDLOCK |
//                          MS_DIRSYNC | MS_NOATIME | MS_NODIRATIME | MS_REC |
//                          MS_VERBOSE | MS_SILENT | MS_POSIXACL | MS_RELATIME |
//                          MS_STRICTATIME | MS_LAZYTIME;
//
// Now we inverse the flag:
//
// inverse_mask ~= mask;
//
// Seccomp will now only intercept these flags if they do not contain any of
// the allowed flags, i.e. we only intercept combinations were a new superblock
// is created.

const seccompNotifyMount = `mount notify [3,0,SCMP_CMP_MASKED_EQ,18446744070422410016]
`

// 5 == BPF_PROG_LOAD
// 8 == BPF_PROG_ATTACH
// 9 == BPF_PROG_DETACH
const seccompNotifyBpf = `bpf notify [0,5,SCMP_CMP_EQ]
bpf notify [0,8,SCMP_CMP_EQ]
bpf notify [0,9,SCMP_CMP_EQ]
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
// This is used rather than instance.Instance to avoid import loops.
type Instance interface {
	Name() string
	Project() api.Project
	ExpandedConfig() map[string]string
	IsPrivileged() bool
	Architecture() int
	RootfsPath() string
	CGroup() (*cgroup.CGroup, error)
	CurrentIdmap() (*idmap.IdmapSet, error)
	DiskIdmap() (*idmap.IdmapSet, error)
	IdmappedStorage(path string, fstype string) idmap.IdmapStorageType
	InsertSeccompUnixDevice(prefix string, m deviceConfig.Device, pid int) error
}

var seccompPath = shared.VarPath("security", "seccomp")

// ProfilePath returns the seccomp path for the instance.
func ProfilePath(c Instance) string {
	return filepath.Join(seccompPath, project.Instance(c.Project().Name, c.Name()))
}

// InstanceNeedsPolicy returns whether the instance needs a policy or not.
func InstanceNeedsPolicy(c Instance) bool {
	config := c.ExpandedConfig()

	// Check for text keys
	keys := []string{
		"raw.seccomp",
		"security.syscalls.allow",
		"security.syscalls.deny",
	}

	for _, k := range keys {
		_, hasKey := config[k]
		if hasKey {
			return true
		}
	}

	// Check for boolean keys that default to false
	keys = []string{
		"security.syscalls.deny_compat",
		"security.syscalls.intercept.mknod",
		"security.syscalls.intercept.sched_setscheduler",
		"security.syscalls.intercept.setxattr",
		"security.syscalls.intercept.sysinfo",
		"security.syscalls.intercept.mount",
		"security.syscalls.intercept.bpf",
	}

	for _, k := range keys {
		if shared.IsTrue(config[k]) {
			return true
		}
	}

	// Check for boolean keys that default to true
	value, ok := config["security.syscalls.deny_default"]
	if !ok || shared.IsTrue(value) {
		return true
	}

	return false
}

// InstanceNeedsIntercept returns whether instance needs intercept.
func InstanceNeedsIntercept(s *state.State, c Instance) (bool, error) {
	// No need if privileged
	if c.IsPrivileged() {
		return false, nil
	}

	// If nested, assume the host handles it
	if s.OS.RunningInUserNS {
		return false, nil
	}

	config := c.ExpandedConfig()

	var keys = map[string]func(state *state.State) error{
		"security.syscalls.intercept.mknod":              lxcSupportSeccompNotify,
		"security.syscalls.intercept.sched_setscheduler": lxcSupportSeccompNotify,
		"security.syscalls.intercept.setxattr":           lxcSupportSeccompNotify,
		"security.syscalls.intercept.sysinfo":            lxcSupportSeccompNotify,
		"security.syscalls.intercept.mount":              lxcSupportSeccompNotifyContinue,
		"security.syscalls.intercept.bpf":                lxcSupportSeccompNotifyAddfd,
	}

	needed := false
	for key, check := range keys {
		if shared.IsFalseOrEmpty(config[key]) {
			continue
		}

		err := check(s)
		if err != nil {
			return needed, err
		}

		needed = true
	}

	if config["linux.kernel_modules.load"] == "ondemand" {
		err := lxcSupportSeccompNotifyContinue(s)
		if err != nil {
			return needed, err
		}

		needed = true
	}

	return needed, nil
}

// MakePidFd prepares a pidfd to inherit for the init process of the container.
func MakePidFd(pid int, s *state.State) (int, *os.File) {
	if s.OS.PidFds {
		pidFdFile, err := linux.PidFdOpen(pid, 0)
		if err != nil {
			return -1, nil
		}

		return 3, pidFdFile
	}

	return -1, nil
}

func seccompGetPolicyContent(s *state.State, c Instance) (string, error) {
	config := c.ExpandedConfig()

	// Full policy override
	raw := config["raw.seccomp"]
	if raw != "" {
		return raw, nil
	}

	// Policy header
	policy := seccompHeader
	allowlist := config["security.syscalls.allow"]

	if allowlist != "" {
		if !s.OS.LXCFeatures["seccomp_allow_deny_syntax"] {
			return "", fmt.Errorf("Unable to configure allowlist, liblxc is does not support: %q", "seccomp_allow_deny_syntax")
		}

		policy += "allowlist\n[all]\n"
		policy += allowlist
	} else {
		if !s.OS.LXCFeatures["seccomp_allow_deny_syntax"] {
			return "", fmt.Errorf("Unable to configure denylist, liblxc is does not support: %q", "seccomp_allow_deny_syntax")
		}

		policy += "denylist\n[all]\n"

		defaultFlag, ok := config["security.syscalls.deny_default"]
		if !ok || shared.IsTrue(defaultFlag) {
			policy += defaultSeccompPolicy
		}
	}

	// Syscall interception
	ok, err := InstanceNeedsIntercept(s, c)
	if err != nil {
		return "", err
	}

	if ok {
		// Prevent the container from overriding our syscall
		// supervision.
		policy += seccompNotifyDisallow

		if shared.IsTrue(config["security.syscalls.intercept.mknod"]) {
			policy += seccompNotifyMknod
		}

		if shared.IsTrue(config["security.syscalls.intercept.sched_setscheduler"]) {
			policy += seccompNotifySchedSetscheduler
		}

		if shared.IsTrue(config["security.syscalls.intercept.setxattr"]) {
			policy += seccompNotifySetxattr
		}

		if shared.IsTrue(config["security.syscalls.intercept.sysinfo"]) {
			policy += seccompNotifySysinfo
		}

		if config["linux.kernel_modules.load"] == "ondemand" {
			policy += seccompNotifyModule
		}

		if shared.IsTrue(config["security.syscalls.intercept.mount"]) {
			policy += seccompNotifyMount
			// We block the new mount api for now to simplify mount
			// syscall interception. Since it keeps state over
			// multiple syscalls we'd need more invasive changes to
			// make this work.
			policy += seccompBlockNewMountAPI
		}

		if shared.IsTrue(config["security.syscalls.intercept.bpf"]) {
			policy += seccompNotifyBpf
		}
	}

	if allowlist != "" {
		return policy, nil
	}

	// Additional deny entries
	compat := config["security.syscalls.deny_compat"]
	if shared.IsTrue(compat) {
		arch, err := osarch.ArchitectureName(c.Architecture())
		if err != nil {
			return "", err
		}

		policy += fmt.Sprintf(compatBlockingPolicy, arch)
	}

	denylist := config["security.syscalls.deny"]
	if denylist != "" {
		policy += denylist
	}

	return policy, nil
}

// CreateProfile creates a seccomp profile.
func CreateProfile(s *state.State, c Instance) error {
	/* Unlike apparmor, there is no way to "cache" profiles, and profiles
	 * are automatically unloaded when a task dies. Thus, we don't need to
	 * unload them when a container stops, and we don't have to worry about
	 * the mtime on the file for any compiler purpose, so let's just write
	 * out the profile.
	 */
	if !InstanceNeedsPolicy(c) {
		return nil
	}

	profile, err := seccompGetPolicyContent(s, c)
	if err != nil {
		return err
	}

	err = os.MkdirAll(seccompPath, 0700)
	if err != nil {
		return err
	}

	return os.WriteFile(ProfilePath(c), []byte(profile), 0600)
}

// DeleteProfile removes a seccomp profile.
func DeleteProfile(c Instance) {
	/* similar to AppArmor, if we've never started this container, the
	 * delete can fail and that's ok.
	 */
	_ = os.Remove(ProfilePath(c))
}

// Server defines a seccomp server.
type Server struct {
	s    *state.State
	path string
	l    net.Listener
}

// Iovec defines an iovec to move data between kernel and userspace.
type Iovec struct {
	ucred    *unix.Ucred
	memFd    int
	procFd   int
	notifyFd int
	msg      *C.struct_seccomp_notify_proxy_msg
	req      *C.struct_seccomp_notif
	resp     *C.struct_seccomp_notif_resp
	cookie   *C.char
	iov      *C.struct_iovec
}

// NewSeccompIovec creates a new seccomp iovec.
func NewSeccompIovec(ucred *unix.Ucred) *Iovec {
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
		memFd:    -1,
		procFd:   -1,
		notifyFd: -1,
		msg:      msg,
		req:      req,
		resp:     resp,
		cookie:   cookie,
		iov:      iov,
		ucred:    ucred,
	}
}

// PutSeccompIovec puts a seccomp iovec.
func (siov *Iovec) PutSeccompIovec() {
	if siov.memFd >= 0 {
		_ = unix.Close(siov.memFd)
	}

	if siov.procFd >= 0 {
		_ = unix.Close(siov.procFd)
	}

	if siov.notifyFd >= 0 {
		_ = unix.Close(siov.notifyFd)
	}

	C.free(unsafe.Pointer(siov.msg))
	C.free(unsafe.Pointer(siov.req))
	C.free(unsafe.Pointer(siov.resp))
	C.free(unsafe.Pointer(siov.cookie))
	C.free(unsafe.Pointer(siov.iov))
}

// ReceiveSeccompIovec receives a seccomp iovec.
func (siov *Iovec) ReceiveSeccompIovec(fd int) (uint64, error) {
	bytes, fds, err := netutils.AbstractUnixReceiveFdData(fd, 3, netutils.UnixFdsAcceptLess, unsafe.Pointer(siov.iov), 4)
	if err != nil || err == io.EOF {
		return 0, err
	}

	siov.procFd = int(fds[0])
	siov.memFd = int(fds[1])
	siov.notifyFd = int(fds[2])
	logger.Debugf("Syscall handler received fds %d(/proc/<pid>), %d(/proc/<pid>/mem), and %d([seccomp notify])", siov.procFd, siov.memFd, siov.notifyFd)

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
			siov.ucred.Pid)
		return false
	}

	if siov.msg.sizes.seccomp_notif != C.expected_sizes.seccomp_notif {
		logger.Warnf("Disconnected from seccomp socket since client uses different seccomp_notif sizes: %d != %d, pid=%v",
			siov.msg.sizes.seccomp_notif, C.expected_sizes.seccomp_notif, siov.ucred.Pid)
		return false
	}

	if siov.msg.sizes.seccomp_notif_resp != C.expected_sizes.seccomp_notif_resp {
		logger.Warnf("Disconnected from seccomp socket since client uses different seccomp_notif_resp sizes: %d != %d, pid=%v",
			siov.msg.sizes.seccomp_notif_resp, C.expected_sizes.seccomp_notif_resp, siov.ucred.Pid)
		return false
	}

	if siov.msg.sizes.seccomp_data != C.expected_sizes.seccomp_data {
		logger.Warnf("Disconnected from seccomp socket since client uses different seccomp_data sizes: %d != %d, pid=%v",
			siov.msg.sizes.seccomp_data, C.expected_sizes.seccomp_data, siov.ucred.Pid)
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

		logger.Debugf("Disconnected from seccomp socket after failed write for process %v: %s", siov.ucred.Pid, err)
		return fmt.Errorf("Failed to send response to seccomp client %v", siov.ucred.Pid)
	}

	if uint64(bytes) != uint64(C.SECCOMP_MSG_SIZE_MIN) {
		logger.Debugf("Disconnected from seccomp socket after short write: pid=%v", siov.ucred.Pid)
		return fmt.Errorf("Failed to send full response to seccomp client %v", siov.ucred.Pid)
	}

	logger.Debugf("Send seccomp notification for id(%d)", siov.resp.id)
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

				logger.Debugf("Connected to seccomp socket: pid=%v", ucred.Pid)

				unixFile, err := c.(*net.UnixConn).File()
				if err != nil {
					logger.Debugf("Failed to turn unix socket client into file")
					return
				}

				for {
					siov := NewSeccompIovec(ucred)
					bytes, err := siov.ReceiveSeccompIovec(int(unixFile.Fd()))
					if err != nil {
						logger.Debugf("Disconnected from seccomp socket after failed receive: pid=%v, err=%s", ucred.Pid, err)
						_ = c.Close()
						return
					}

					if siov.IsValidSeccompIovec(bytes) {
						go func() { _ = server.HandleValid(int(unixFile.Fd()), siov, findPID) }()
					} else {
						go server.HandleInvalid(int(unixFile.Fd()), siov)
					}
				}
			}()
		}
	}()

	return &server, nil
}

// TaskIDs returns the task IDs for a process.
func TaskIDs(pid int) (UID int64, GID int64, fsUID int64, fsGID int64, err error) {
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return -1, -1, -1, -1, err
	}

	reUID, err := regexp.Compile(`^Uid:\s+([0-9]+)\s+([0-9]+)\s+([0-9]+)\s+([0-9]+)`)
	if err != nil {
		return -1, -1, -1, -1, err
	}

	reGID, err := regexp.Compile(`^Gid:\s+([0-9]+)\s+([0-9]+)\s+([0-9]+)\s+([0-9]+)`)
	if err != nil {
		return -1, -1, -1, -1, err
	}

	UID = -1
	GID = -1
	fsUID = -1
	fsGID = -1
	UIDFound := false
	GIDFound := false
	for _, line := range strings.Split(string(status), "\n") {
		if UIDFound && GIDFound {
			break
		}

		if !UIDFound {
			m := reUID.FindStringSubmatch(line)
			if len(m) > 2 {
				// effective uid
				result, err := strconv.ParseInt(m[2], 10, 64)
				if err != nil {
					return -1, -1, -1, -1, err
				}

				UID = result
				UIDFound = true
			}

			if len(m) > 4 {
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
			if len(m) > 2 {
				// effective gid
				result, err := strconv.ParseInt(m[2], 10, 64)
				if err != nil {
					return -1, -1, -1, -1, err
				}

				GID = result
				GIDFound = true
			}

			if len(m) > 4 {
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

// FindTGID returns the task group leader ID from /proc/<pid> fd
func FindTGID(procFd int) (uint32, error) {
	var statusFile *os.File
	fd, err := unix.Openat(procFd, "status", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}

	statusFile = os.NewFile(uintptr(fd), "/proc/<pid>/status")
	status, err := io.ReadAll(statusFile)
	_ = statusFile.Close()
	if err != nil {
		return 0, err
	}

	reTGID, err := regexp.Compile(`^Tgid:\s+([0-9]+)`)
	if err != nil {
		return 0, err
	}

	for _, line := range strings.Split(string(status), "\n") {
		m := reTGID.FindStringSubmatch(line)
		if len(m) > 1 {
			result, err := strconv.ParseUint(m[1], 10, 32)
			if err != nil {
				return 0, err
			}

			return uint32(result), nil
		}
	}

	return 0, fmt.Errorf("Task group leader ID not found")
}

// isCapableInCtInitUserns checks if intercepted syscall's caller has a (cap) effective
// capability in the container's initial user namespace
func isCapableInCtInitUserns(siov *Iovec, cap C.int) (bool, error) {
	containerInitPID := int(siov.msg.init_pid)
	targetPID := int(siov.req.pid)

	ctInitUserNS, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/user", containerInitPID))
	if err != nil {
		return false, fmt.Errorf("Can't get userns for container's init process: %w", err)
	}

	reqUserNS, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/user", targetPID))
	if err != nil {
		return false, fmt.Errorf("Can't get userns for requestor process: %w", err)
	}

	// Ensure that requestor process is in the initial container's userns
	if ctInitUserNS != reqUserNS {
		return false, nil
	}

	return bool(C.lxd_pid_cap_is_set(C.int(targetPID), cap, C.CAP_EFFECTIVE)), nil
}

// CallForkmknod executes fork mknod.
func CallForkmknod(c Instance, dev deviceConfig.Device, requestPID int, s *state.State) int {
	uid, gid, fsuid, fsgid, err := TaskIDs(requestPID)
	if err != nil {
		return int(-C.EPERM)
	}

	pidFdNr, pidFd := MakePidFd(requestPID, s)
	if pidFdNr >= 0 {
		defer func() { _ = pidFd.Close() }()
	}

	_, stderr, err := shared.RunCommandSplit(
		context.TODO(),
		nil,
		[]*os.File{pidFd},
		util.GetExecPath(),
		"forksyscall",
		"mknod",
		dev["pid"],
		fmt.Sprintf("%d", pidFdNr),
		dev["path"],
		dev["mode_t"],
		dev["dev_t"],
		fmt.Sprintf("%d", uid),
		fmt.Sprintf("%d", gid),
		fmt.Sprintf("%d", fsuid),
		fmt.Sprintf("%d", fsgid))
	if err != nil {
		errno, err := strconv.Atoi(stderr)
		if err != nil || errno == C.ENOANO {
			return int(-C.EPERM)
		}

		return -errno
	}

	return 0
}

// HandleInvalid sends a placeholder message to LXC. LXC will notice the short write
// and send a default message to the kernel thereby avoiding a 30s block.
func (s *Server) HandleInvalid(fd int, siov *Iovec) {
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
	l := logger.AddContext(logger.Ctx{
		"project":  c.Project().Name,
		"instance": c.Name(),
	})

	dev := deviceConfig.Device{}
	dev["type"] = "unix-char"
	dev["mode"] = fmt.Sprintf("%#o", args.cMode)
	dev["major"] = fmt.Sprintf("%d", unix.Major(uint64(args.cDev)))
	dev["minor"] = fmt.Sprintf("%d", unix.Minor(uint64(args.cDev)))
	dev["pid"] = fmt.Sprintf("%d", args.cPid)
	dev["path"] = args.path
	dev["mode_t"] = fmt.Sprintf("%d", args.cMode)
	dev["dev_t"] = fmt.Sprintf("%d", args.cDev)

	// has CAP_MKNOD capability?
	hasCapability, err := isCapableInCtInitUserns(siov, C.CAP_MKNOD)
	if err != nil {
		l.Error(fmt.Sprintf("%v", err))
		return int(-C.EPERM)
	}

	if !hasCapability {
		l.Error("Requestor process creds lacks CAP_MKNOD")
		return int(-C.EPERM)
	}

	errno := CallForkmknod(c, dev, int(args.cPid), s.s)
	if errno != int(-C.ENOMEDIUM) {
		return errno
	}

	l.Debug("Using fallback codepath for mknod syscall interception...")
	err = c.InsertSeccompUnixDevice(fmt.Sprintf("forkmknod.unix.%d", int(args.cPid)), dev, int(args.cPid))
	if err != nil {
		return int(-C.EPERM)
	}

	return 0
}

// HandleMknodSyscall handles a mknod syscall.
func (s *Server) HandleMknodSyscall(c Instance, siov *Iovec) int {
	ctx := logger.Ctx{"container": c.Name(),
		"project":               c.Project().Name,
		"syscall_number":        siov.req.data.nr,
		"audit_architecture":    siov.req.data.arch,
		"seccomp_notify_id":     siov.req.id,
		"seccomp_notify_flags":  siov.req.flags,
		"seccomp_notify_pid":    siov.req.pid,
		"seccomp_notify_fd":     siov.notifyFd,
		"seccomp_notify_mem_fd": siov.memFd,
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
	ctx := logger.Ctx{"container": c.Name(),
		"project":               c.Project().Name,
		"syscall_number":        siov.req.data.nr,
		"audit_architecture":    siov.req.data.arch,
		"seccomp_notify_id":     siov.req.id,
		"seccomp_notify_flags":  siov.req.flags,
		"seccomp_notify_pid":    siov.req.pid,
		"seccomp_notify_fd":     siov.notifyFd,
		"seccomp_notify_mem_fd": siov.memFd,
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
	ctx := logger.Ctx{"container": c.Name(),
		"project":               c.Project().Name,
		"syscall_number":        siov.req.data.nr,
		"audit_architecture":    siov.req.data.arch,
		"seccomp_notify_id":     siov.req.id,
		"seccomp_notify_flags":  siov.req.flags,
		"seccomp_notify_pid":    siov.req.pid,
		"seccomp_notify_fd":     siov.notifyFd,
		"seccomp_notify_mem_fd": siov.memFd,
	}

	defer logger.Debug("Handling setxattr syscall", ctx)

	args := SetxattrArgs{}

	args.pid = int(siov.req.pid)

	pidFdNr, pidFd := MakePidFd(args.pid, s.s)
	if pidFdNr >= 0 {
		defer func() { _ = pidFd.Close() }()
	}

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

	_, stderr, err := shared.RunCommandSplit(
		context.TODO(),
		nil,
		[]*os.File{pidFd},
		util.GetExecPath(),
		"forksyscall",
		"setxattr",
		fmt.Sprintf("%d", args.pid),
		fmt.Sprintf("%d", pidFdNr),
		fmt.Sprintf("%d", args.nsuid),
		fmt.Sprintf("%d", args.nsgid),
		fmt.Sprintf("%d", args.nsfsuid),
		fmt.Sprintf("%d", args.nsfsgid),
		args.name,
		args.path,
		fmt.Sprintf("%d", args.flags),
		fmt.Sprintf("%d", whiteout),
		fmt.Sprintf("%d", args.size),
		string(args.value))
	if err != nil {
		errno, err := strconv.Atoi(stderr)
		if err != nil || errno == C.ENOANO {
			return int(-C.EPERM)
		}

		return -errno
	}

	return 0
}

// SchedSetschedulerArgs arguments for setxattr.
type SchedSetschedulerArgs struct {
	switchPidns   int
	pidCaller     int
	pidTarget     int
	policy        C.int
	schedPriority C.int
	nsuid         int64
	nsgid         int64
}

// HandleSchedSetschedulerSyscall handles sched_setscheduler syscalls.
func (s *Server) HandleSchedSetschedulerSyscall(c Instance, siov *Iovec) int {
	ctx := logger.Ctx{"container": c.Name(),
		"project":               c.Project().Name,
		"syscall_number":        siov.req.data.nr,
		"audit_architecture":    siov.req.data.arch,
		"seccomp_notify_id":     siov.req.id,
		"seccomp_notify_flags":  siov.req.flags,
		"seccomp_notify_pid":    siov.req.pid,
		"seccomp_notify_fd":     siov.notifyFd,
		"seccomp_notify_mem_fd": siov.memFd,
	}

	defer logger.Debug("Handling sched_setscheduler syscall", ctx)

	args := SchedSetschedulerArgs{}
	args.pidCaller = int(siov.req.pid)

	pidFdNr, pidFd := MakePidFd(args.pidCaller, s.s)
	if pidFdNr >= 0 {
		defer func() { _ = pidFd.Close() }()
	}

	uid, gid, _, _, err := TaskIDs(args.pidCaller)
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

	// Only care about userns root for now.
	args.nsuid, args.nsgid = idmapset.ShiftFromNs(uid, gid)
	if args.nsuid != 0 || args.nsgid != 0 {
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(-C.EINVAL)
	}

	// The target pid is only valid in the container's pid namespace as
	// we're taking it from the raw system call arguments.
	args.pidTarget = int(siov.req.data.args[0])
	if args.pidTarget < 0 {
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(-C.EINVAL)
	}

	// If the caller passed zero they want to change their own attributes.
	if args.pidTarget == 0 {
		// This pid is relative to our pid namespace so we need to
		// inform forksyscall to not switch pid namespaces when
		// emulating the system call.
		args.pidTarget = args.pidCaller
		args.switchPidns = 0
	} else {
		// The pid is relative to the container's pid namespace.
		args.switchPidns = 1
	}

	// error out if policy < 0
	if args.policy < 0 {
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(-C.EINVAL)
	}

	// int policy
	args.policy = C.int(siov.req.data.args[1])

	schedParamArgs := C.struct_sched_param{}
	_, err = C.pread(C.int(siov.memFd), unsafe.Pointer(&schedParamArgs), C.LXD_SCHED_PARAM_SIZE, C.off_t(siov.req.data.args[2]))
	if err != nil {
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(-C.EPERM)
	}

	args.schedPriority = schedParamArgs.sched_priority

	_, stderr, err := shared.RunCommandSplit(
		context.TODO(),
		nil,
		[]*os.File{pidFd},
		util.GetExecPath(),
		"forksyscall",
		"sched_setscheduler",
		fmt.Sprintf("%d", args.pidCaller),
		fmt.Sprintf("%d", pidFdNr),
		fmt.Sprintf("%d", args.switchPidns),
		fmt.Sprintf("%d", args.pidTarget),
		fmt.Sprintf("%d", args.policy),
		fmt.Sprintf("%d", args.schedPriority),
	)
	if err != nil {
		errno, err := strconv.Atoi(stderr)
		if err != nil || errno == C.ENOANO {
			return int(-C.EPERM)
		}

		return -errno
	}

	return 0
}

// HandleSysinfoSyscall handles sysinfo syscalls.
func (s *Server) HandleSysinfoSyscall(c Instance, siov *Iovec) int {
	l := logger.AddContext(logger.Ctx{
		"container":             c.Name(),
		"project":               c.Project().Name,
		"syscall_number":        siov.req.data.nr,
		"audit_architecture":    siov.req.data.arch,
		"seccomp_notify_id":     siov.req.id,
		"seccomp_notify_flags":  siov.req.flags,
		"seccomp_notify_pid":    siov.req.pid,
		"seccomp_notify_fd":     siov.notifyFd,
		"seccomp_notify_mem_fd": siov.memFd,
	})

	defer l.Debug("Handling sysinfo syscall")

	// Pre-fill sysinfo struct with metrics from host system.
	info := unix.Sysinfo_t{}
	err := unix.Sysinfo(&info)
	if err != nil {
		l.Warn("Failed getting sysinfo", logger.Ctx{"err": err})
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))

		return 0
	}

	instMetrics := Sysinfo{} // Architecture independent place to hold instance metrics.

	cg, err := cgroup.NewFileReadWriter(int(siov.msg.init_pid), liblxc.HasAPIExtension("cgroup2"))
	if err != nil {
		l.Warn("Failed loading cgroup", logger.Ctx{"err": err, "pid": siov.msg.init_pid})
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))

		return 0
	}

	// Get instance uptime.
	pidStat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", siov.msg.init_pid))
	if err != nil {
		l.Warn("Failed getting init process info", logger.Ctx{"err": err, "pid": siov.msg.init_pid})
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))

		return 0
	}

	fields := strings.Fields(string(pidStat))
	tickValue, err := strconv.ParseInt(fields[21], 10, 64)
	if err != nil {
		l.Warn("Failed parsing init process info", logger.Ctx{"err": err, "pid": siov.msg.init_pid})
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))

		return 0
	}

	age := float64(tickValue / 100)
	if age > 0 {
		instMetrics.Uptime = int64(time.Since(s.s.OS.BootTime).Seconds() - age)
	}

	// Get instance process count.
	pids, err := cg.GetProcessesUsage()
	if err != nil {
		l.Warn("Failed getting process count", logger.Ctx{"err": err})
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))

		return 0
	}

	instMetrics.Procs = uint16(pids)

	// Get instance memory stats.
	memStats, err := cg.GetMemoryStats()
	if err != nil {
		l.Warn("Failed getting memory stats", logger.Ctx{"err": err})
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))

		return 0
	}

	for k, v := range memStats {
		switch k {
		case "shmem":
			instMetrics.Sharedram = v
		case "cache":
			instMetrics.Bufferram = v
		}
	}

	// Get instance memory limit.
	memoryLimit, err := cg.GetEffectiveMemoryLimit()
	if err != nil {
		l.Warn("Failed getting effective memory limit", logger.Ctx{"err": err})
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))

		return 0
	}

	// Get instance memory usage.
	memoryUsage, err := cg.GetMemoryUsage()
	if err != nil {
		l.Warn("Failed to get memory usage", logger.Ctx{"err": err})
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))

		return 0
	}

	instMetrics.Totalram = uint64(memoryLimit)
	instMetrics.Freeram = instMetrics.Totalram - uint64(memoryUsage) - instMetrics.Bufferram

	// Get instance swap info.
	if s.s.OS.CGInfo.Supports(cgroup.MemorySwapUsage, cg) {
		swapLimit, err := cg.GetMemorySwapLimit()
		if err != nil {
			l.Warn("Failed getting swap limit", logger.Ctx{"err": err})
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))

			return 0
		}

		swapUsage, err := cg.GetMemorySwapUsage()
		if err != nil {
			l.Warn("Failed getting swap usage", logger.Ctx{"err": err})
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))

			return 0
		}

		instMetrics.Totalswap = uint64(swapLimit)
		instMetrics.Freeswap = instMetrics.Totalswap - uint64(swapUsage)
	}

	// Get writable pointer to buffer of sysinfo syscall result.
	const sz = int(unsafe.Sizeof(info))
	var b []byte = (*(*[sz]byte)(unsafe.Pointer(&info)))[:]

	// Write instance metrics to native sysinfo struct.
	instMetrics.ToNative(&info)

	// Write sysinfo response into buffer.
	_, err = unix.Pwrite(siov.memFd, b, int64(siov.req.data.args[0]))
	if err != nil {
		l.Warn("Failed writing sysinfo", logger.Ctx{"err": err})
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))

		return 0
	}

	return 0
}

type nullWriteCloser struct {
	*bytes.Buffer
}

// Close is a no-op closer implementation
func (nwc *nullWriteCloser) Close() error {
	return nil
}

// HandleFinitModuleSyscall handles finit_module syscalls.
func (s *Server) HandleFinitModuleSyscall(c Instance, siov *Iovec) int {
	ctx := logger.Ctx{"container": c.Name(),
		"project":               c.Project().Name,
		"syscall_number":        siov.req.data.nr,
		"audit_architecture":    siov.req.data.arch,
		"seccomp_notify_id":     siov.req.id,
		"seccomp_notify_flags":  siov.req.flags,
		"seccomp_notify_pid":    siov.req.pid,
		"seccomp_notify_fd":     siov.notifyFd,
		"seccomp_notify_mem_fd": siov.memFd,
	}

	defer logger.Debug("Handling finit_module syscall", ctx)

	// int fd
	fd := C.int(siov.req.data.args[0])

	// const char *param_values
	cBuf := [4096]C.char{}
	_, err := C.pread(C.int(siov.memFd), unsafe.Pointer(&cBuf[0]), C.size_t(4096), C.off_t(siov.req.data.args[1]))
	if err != nil {
		ctx["err"] = fmt.Sprintf("Failed to read memory for finit_module syscall: %s", err)
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(-C.EFAULT)
	}

	paramValues := C.GoString(&cBuf[0])

	// disallow specifying module parameters, because
	// for some modules parameters can be dangerous.
	// Some modules have parameters like "debug" which can
	// turn on dangerous debugging features or create performance issues.
	if string(paramValues) != "" {
		ctx["err"] = "Forbid setting module parameters"
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(-C.EPERM)
	}

	// int flags
	flags := C.int(siov.req.data.args[2])

	// disallow specifying flags, because it's not safe:
	// - MODULE_INIT_IGNORE_MODVERSIONS
	// - MODULE_INIT_IGNORE_VERMAGIC
	// this can allow to load a kernel module which is incompatible
	// with the running kernel and trigger host system crash.
	if flags != 0 {
		ctx["err"] = "Forbid setting flags in finit_module()"
		if s.s.OS.SeccompListenerContinue {
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}

		return int(-C.EPERM)
	}

	ctx["param_values"] = string(paramValues)

	// has CAP_SYS_MODULE capability?
	hasCapability, err := isCapableInCtInitUserns(siov, C.CAP_SYS_MODULE)
	if err != nil {
		ctx["err"] = fmt.Sprintf("%v", err)
		return int(-C.EPERM)
	}

	if !hasCapability {
		ctx["err"] = "Requestor process creds lacks CAP_SYS_MODULE"
		return int(-C.EPERM)
	}

	moduleFileFD, err := unix.Openat(siov.procFd, fmt.Sprintf("fd/%d", fd), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		ctx["err"] = fmt.Sprintf("Can't open module file (unix.Openat): %v", err)
		return int(-C.EPERM)
	}

	moduleFile := os.NewFile(uintptr(moduleFileFD), "/proc/<pid>/fd/<fd>")
	if moduleFile == nil {
		ctx["err"] = fmt.Sprintf("Can't open module file (os.NewFile): %v", err)
		return int(-C.EPERM)
	}

	defer func() { _ = moduleFile.Close() }()

	forksyscallgoargs := []string{
		"forksyscallgo",
		"finit_module_parse",                 // <syscall_operation>
		fmt.Sprintf("%d", int(siov.req.pid)), // <PID>
		fmt.Sprintf("%d", 0),                 // <PidFd>
		fmt.Sprintf("%d", 3),                 // <module_fd>
	}

	var buffer bytes.Buffer
	var output bytes.Buffer
	p := subprocess.NewProcessWithFds(util.GetExecPath(), forksyscallgoargs, nil,
		&nullWriteCloser{&output}, &nullWriteCloser{&buffer})

	// We want to drop privileges, as we use "debug/elf" Golang
	// package which is considered as not safe for production use.
	// By spawning an unprivileged process we can safely use this package.
	p.SetCreds(s.s.OS.UnprivUID, s.s.OS.UnprivGID)

	// Let's set timeout for the helper process.
	// It's just a precautionary measure to prevent blocakge of a syscall interception
	// daemon if anything goes wrong inside the debug/elf Golang package internals.
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	err = p.StartWithFiles(timeoutCtx, []*os.File{moduleFile})
	if err != nil {
		ctx["err"] = fmt.Sprintf("Can't open module file: %v", err)
		return int(-C.EPERM)
	}

	exitCode, err := p.Wait(context.TODO())
	if err != nil || exitCode != 0 {
		ctx["err"] = fmt.Sprintf("forksyscallgo finit_module_parse helper failed. Err: %v exitCode: %d stdout: %s stderr: %s",
			err, exitCode, output.String(), buffer.String())
		return int(-C.EPERM)
	}

	moduleName := output.String()
	ctx["module_name"] = moduleName

	// Check that this module is in the allowlist
	inAllowList := false
	kernelModules := c.ExpandedConfig()["linux.kernel_modules"]
	if kernelModules != "" {
		for _, module := range strings.Split(kernelModules, ",") {
			module = strings.TrimPrefix(module, " ")

			if module == moduleName {
				inAllowList = true
			}
		}
	}

	if !inAllowList {
		ctx["err"] = fmt.Sprintf("Module %q is not in allowlist (linux.kernel_modules)", moduleName)
		return int(-C.EPERM)
	}

	if shared.PathExists(fmt.Sprintf("/sys/module/%s", moduleName)) {
		return int(-C.EEXIST)
	}

	err = util.LoadModule(moduleName)
	if err != nil {
		ctx["err"] = fmt.Sprintf("Failed to load module %q: %v", moduleName, err)
		return int(-C.EPERM)
	}

	return 0
}

// MountArgs arguments for mount.
type MountArgs struct {
	source    string
	target    string
	fstype    string
	flags     int
	data      string
	pid       int
	idmapType idmap.IdmapStorageType
	uid       int64
	gid       int64
	fsuid     int64
	fsgid     int64
	nsuid     int64
	nsgid     int64
	nsfsuid   int64
	nsfsgid   int64
}

const knownFlags C.ulong = C.MS_BIND | C.MS_LAZYTIME | C.MS_MANDLOCK |
	C.MS_NOATIME | C.MS_NODEV | C.MS_NODIRATIME |
	C.MS_NOEXEC | C.MS_NOSUID | C.MS_REMOUNT |
	C.MS_RDONLY | C.MS_STRICTATIME |
	C.MS_SYNCHRONOUS | C.MS_BIND
const knownFlagsRecursive C.ulong = knownFlags | C.MS_REC

var mountFlagsToOptMap = map[C.ulong]string{
	C.MS_BIND:            "bind",
	C.ulong(0):           "defaults",
	C.MS_LAZYTIME:        "lazytime",
	C.MS_MANDLOCK:        "mand",
	C.MS_NOATIME:         "noatime",
	C.MS_NODEV:           "nodev",
	C.MS_NODIRATIME:      "nodiratime",
	C.MS_NOEXEC:          "noexec",
	C.MS_NOSUID:          "nosuid",
	C.MS_REMOUNT:         "remount",
	C.MS_RDONLY:          "ro",
	C.MS_STRICTATIME:     "strictatime",
	C.MS_SYNCHRONOUS:     "sync",
	C.MS_REC | C.MS_BIND: "rbind",
}

func mountFlagsToOpts(flags C.ulong) string {
	var currentBit C.ulong
	opts := ""
	var msRec C.ulong = (flags & C.MS_REC)

	flags = (flags &^ C.MS_REC)
	for currentBit < (4*8 - 1) {
		var flag C.ulong = (1 << currentBit)

		currentBit++

		if (flags & flag) == 0 {
			continue
		}

		if (flag == C.MS_BIND) && msRec == C.MS_REC {
			flag |= msRec
		}

		optOrArg := mountFlagsToOptMap[flag]

		if optOrArg == "" {
			continue
		}

		if opts == "" {
			opts = optOrArg
		} else {
			opts = fmt.Sprintf("%s,%s", opts, optOrArg)
		}
	}

	return opts
}

// mountHandleHugetlbfsArgs adds user namespace root uid and gid to the
// hugetlbfs mount options to make it useable in unprivileged containers.
func (s *Server) mountHandleHugetlbfsArgs(c Instance, args *MountArgs, nsuid int64, nsgid int64) error {
	if args.fstype != "hugetlbfs" {
		return nil
	}

	if args.data == "" {
		args.data = fmt.Sprintf("uid=%d,gid=%d", nsuid, nsgid)
		return nil
	}

	uidOpt := int64(-1)
	gidOpt := int64(-1)

	idmapset, err := c.CurrentIdmap()
	if err != nil {
		return err
	}

	optStrings := strings.Split(args.data, ",")
	for i, optString := range optStrings {
		if strings.HasPrefix(optString, "uid=") {
			uidFields := strings.Split(optString, "=")
			if len(uidFields) > 1 {
				n, err := strconv.ParseInt(uidFields[1], 10, 64)
				if err != nil {
					// If the user specified garbage, let the kernel tell em whats what.
					return nil
				}

				uidOpt, _ = idmapset.ShiftIntoNs(n, 0)
				if uidOpt < 0 {
					// If the user specified garbage, let the kernel tell em whats what.
					return nil
				}

				optStrings[i] = fmt.Sprintf("uid=%d", uidOpt)
			}
		} else if strings.HasPrefix(optString, "gid=") {
			gidFields := strings.Split(optString, "=")
			if len(gidFields) > 1 {
				n, err := strconv.ParseInt(gidFields[1], 10, 64)
				if err != nil {
					// If the user specified garbage, let the kernel tell em whats what.
					return nil
				}

				gidOpt, _ = idmapset.ShiftIntoNs(n, 0)
				if gidOpt < 0 {
					// If the user specified garbage, let the kernel tell em whats what.
					return nil
				}

				optStrings[i] = fmt.Sprintf("gid=%d", gidOpt)
			}
		}
	}

	if uidOpt == -1 {
		optStrings = append(optStrings, fmt.Sprintf("uid=%d", nsuid))
	}

	if gidOpt == -1 {
		optStrings = append(optStrings, fmt.Sprintf("gid=%d", nsgid))
	}

	args.data = strings.Join(optStrings, ",")
	args.idmapType = idmap.IdmapStorageNone
	return nil
}

// HandleMountSyscall handles mount syscalls.
func (s *Server) HandleMountSyscall(c Instance, siov *Iovec) int {
	ctx := logger.Ctx{"container": c.Name(),
		"project":               c.Project().Name,
		"syscall_number":        siov.req.data.nr,
		"audit_architecture":    siov.req.data.arch,
		"seccomp_notify_id":     siov.req.id,
		"seccomp_notify_flags":  siov.req.flags,
		"seccomp_notify_pid":    siov.req.pid,
		"seccomp_notify_fd":     siov.notifyFd,
		"seccomp_notify_mem_fd": siov.memFd,
	}

	defer logger.Debug("Handling mount syscall", ctx)

	args := MountArgs{
		pid: int(siov.req.pid),
	}

	pidFdNr, pidFd := MakePidFd(args.pid, s.s)
	if pidFdNr >= 0 {
		defer func() { _ = pidFd.Close() }()
	}

	mntSource := [unix.PathMax]C.char{}
	mntTarget := [unix.PathMax]C.char{}
	mntFs := [unix.PathMax]C.char{}
	mntData := [unix.PathMax]C.char{}

	// const char *source
	if siov.req.data.args[0] != 0 {
		_, err := C.pread(C.int(siov.memFd), unsafe.Pointer(&mntSource[0]), C.size_t(unix.PathMax), C.off_t(siov.req.data.args[0]))
		if err != nil {
			ctx["err"] = fmt.Sprintf("Failed to read source path for of mount syscall: %s", err)
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}
	}
	args.source = C.GoString(&mntSource[0])
	ctx["source"] = args.source

	// const char *target
	if siov.req.data.args[1] != 0 {
		_, err := C.pread(C.int(siov.memFd), unsafe.Pointer(&mntTarget[0]), C.size_t(unix.PathMax), C.off_t(siov.req.data.args[1]))
		if err != nil {
			ctx["err"] = fmt.Sprintf("Failed to read target path for of mount syscall: %s", err)
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}
	}
	args.target = C.GoString(&mntTarget[0])
	ctx["target"] = args.target

	// const char *filesystemtype
	if siov.req.data.args[1] != 0 {
		_, err := C.pread(C.int(siov.memFd), unsafe.Pointer(&mntFs[0]), C.size_t(unix.PathMax), C.off_t(siov.req.data.args[2]))
		if err != nil {
			ctx["err"] = fmt.Sprintf("Failed to read fstype for of mount syscall: %s", err)
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}
	}
	args.fstype = C.GoString(&mntFs[0])
	ctx["fstype"] = args.fstype

	// idmap shift
	fullSrcPath := filepath.Join(fmt.Sprintf("/proc/%d/root/", args.pid), args.source)
	if shared.PathExists(fullSrcPath) {
		args.idmapType = s.MountSyscallShift(c, fullSrcPath)
	} else {
		args.idmapType = s.MountSyscallShift(c, args.source)
	}

	// unsigned long mountflags
	args.flags = int(siov.req.data.args[3])

	// const void *data
	if siov.req.data.args[4] != 0 {
		_, err := C.pread(C.int(siov.memFd), unsafe.Pointer(&mntData[0]), C.size_t(unix.PathMax), C.off_t(siov.req.data.args[4]))
		if err != nil {
			ctx["err"] = fmt.Sprintf("Failed to read mount data for of mount syscall: %s", err)
			ctx["syscall_continue"] = "true"
			C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
			return 0
		}
	}
	args.data = C.GoString(&mntData[0])
	ctx["data"] = args.data

	err := linux.PidfdSendSignal(int(pidFd.Fd()), 0, 0)
	if err != nil {
		ctx["err"] = fmt.Sprintf("Failed to send signal to target process for of mount syscall: %s", err)
		ctx["syscall_continue"] = "true"
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
		return 0
	}

	ok, fuseBinary := s.MountSyscallValid(c, &args)
	if !ok {
		ctx["syscall_continue"] = "true"
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
		return 0
	}

	idmapset, err := c.CurrentIdmap()
	if err != nil {
		ctx["syscall_continue"] = "true"
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
		return 0
	}

	args.uid, args.gid, args.fsuid, args.fsgid, err = TaskIDs(args.pid)
	if err != nil {
		ctx["syscall_continue"] = "true"
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
		return 0
	}

	ctx["host_uid"] = args.uid
	ctx["host_gid"] = args.gid
	ctx["host_fsuid"] = args.fsuid
	ctx["host_fsgid"] = args.fsgid

	args.nsuid, args.nsgid = idmapset.ShiftFromNs(args.uid, args.gid)
	args.nsfsuid, args.nsfsgid = idmapset.ShiftFromNs(args.fsuid, args.fsgid)
	ctx["ns_uid"] = args.nsuid
	ctx["ns_gid"] = args.nsgid
	ctx["ns_fsuid"] = args.nsfsuid
	ctx["ns_fsgid"] = args.nsfsgid

	err = s.mountHandleHugetlbfsArgs(c, &args, args.uid, args.gid)
	if err != nil {
		ctx["syscall_continue"] = "true"
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
		return 0
	}

	if fuseBinary != "" {
		// Record ignored flags for debugging purposes
		flags := C.ulong(args.flags)
		ctx["fuse_ignored_flags"] = fmt.Sprintf("%x", (flags &^ (knownFlagsRecursive | C.MS_MGC_MSK)))

		addOpts := mountFlagsToOpts(flags)

		fuseSource := fmt.Sprintf("%s#%s", fuseBinary, args.source)
		fuseOpts := ""
		if args.data != "" && addOpts != "" {
			fuseOpts = fmt.Sprintf("%s,%s", args.data, addOpts)
		} else if args.data != "" {
			fuseOpts = args.data
		} else if addOpts != "" {
			fuseOpts = addOpts
		}

		ctx["fuse_source"] = fuseSource
		ctx["fuse_target"] = args.target
		ctx["fuse_opts"] = fuseOpts
		_, _, err = shared.RunCommandSplit(
			context.TODO(),
			nil,
			[]*os.File{pidFd},
			util.GetExecPath(),
			"forksyscall",
			"mount",
			fmt.Sprintf("%d", args.pid),
			fmt.Sprintf("%d", pidFdNr),
			fmt.Sprintf("%d", 1),
			fmt.Sprintf("%d", args.uid),
			fmt.Sprintf("%d", args.gid),
			fmt.Sprintf("%d", args.fsuid),
			fmt.Sprintf("%d", args.fsgid),
			fuseSource,
			args.target,
			fuseOpts)
	} else {
		_, _, err = shared.RunCommandSplit(
			context.TODO(),
			nil,
			[]*os.File{pidFd},
			util.GetExecPath(),
			"forksyscall",
			"mount",
			fmt.Sprintf("%d", args.pid),
			fmt.Sprintf("%d", pidFdNr),
			fmt.Sprintf("%d", 0),
			args.source,
			args.target,
			args.fstype,
			fmt.Sprintf("%d", args.flags),
			string(args.idmapType),
			fmt.Sprintf("%d", args.uid),
			fmt.Sprintf("%d", args.gid),
			fmt.Sprintf("%d", args.fsuid),
			fmt.Sprintf("%d", args.fsgid),
			fmt.Sprintf("%d", args.nsuid),
			fmt.Sprintf("%d", args.nsgid),
			fmt.Sprintf("%d", args.nsfsuid),
			fmt.Sprintf("%d", args.nsfsgid),
			args.data)
	}

	if err != nil {
		ctx["syscall_continue"] = "true"
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
		return 0
	}

	return 0
}

// HandleBpfSyscall handles mount syscalls.
func (s *Server) HandleBpfSyscall(c Instance, siov *Iovec) int {
	ctx := logger.Ctx{"container": c.Name(),
		"project":               c.Project().Name,
		"syscall_number":        siov.req.data.nr,
		"audit_architecture":    siov.req.data.arch,
		"seccomp_notify_id":     siov.req.id,
		"seccomp_notify_flags":  siov.req.flags,
		"seccomp_notify_pid":    siov.req.pid,
		"seccomp_notify_fd":     siov.notifyFd,
		"seccomp_notify_mem_fd": siov.memFd,
	}

	defer logger.Debug("Handling bpf syscall", ctx)
	var bpfCmd, bpfProgType, bpfAttachType C.int

	if shared.IsFalseOrEmpty(c.ExpandedConfig()["security.syscalls.intercept.bpf.devices"]) {
		ctx["syscall_continue"] = "true"
		ctx["syscall_handler_reason"] = "No bpf policy specified"
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
		return 0
	}

	tgid, err := FindTGID(siov.procFd)
	if err != nil {
		ctx["syscall_continue"] = "true"
		ctx["syscall_handler_reason"] = "Could not find thread group leader ID"
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
		return 0
	}

	// Locking to a thread shouldn't be necessary but it still makes me
	// queezy that Go could just wander off to somehwere.
	runtime.LockOSThread()
	ret := C.handle_bpf_syscall(
		C.pid_t(siov.req.pid),
		C.int(siov.notifyFd),
		C.int(siov.memFd),
		C.int(tgid),
		siov.msg,
		siov.req,
		siov.resp,
		&bpfCmd,
		&bpfProgType,
		&bpfAttachType)
	runtime.UnlockOSThread()
	ctx["bpf_cmd"] = fmt.Sprintf("%d", bpfCmd)
	ctx["bpf_prog_type"] = fmt.Sprintf("%d", bpfProgType)
	ctx["bpf_attach_type"] = fmt.Sprintf("%d", bpfAttachType)
	if ret < 0 {
		ctx["syscall_continue"] = "true"
		ctx["syscall_handler_error"] = fmt.Sprintf("%s - Failed to handle bpf syscall", unix.Errno(-ret))
		C.seccomp_notify_update_response(siov.resp, 0, C.uint32_t(seccompUserNotifFlagContinue))
		return 0
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
	case lxdSeccompNotifyMount:
		return s.HandleMountSyscall(c, siov)
	case lxdSeccompNotifyBpf:
		return s.HandleBpfSyscall(c, siov)
	case lxdSeccompNotifySchedSetscheduler:
		return s.HandleSchedSetschedulerSyscall(c, siov)
	case lxdSeccompNotifySysinfo:
		return s.HandleSysinfoSyscall(c, siov)
	case lxdSeccompNotifyFinitModule:
		return s.HandleFinitModuleSyscall(c, siov)
	}

	return int(-C.EINVAL)
}

const seccompUserNotifFlagContinue uint32 = 0x00000001

// HandleValid handles a valid seccomp notifier message.
func (s *Server) HandleValid(fd int, siov *Iovec, findPID func(pid int32, state *state.State) (Instance, error)) error {
	defer siov.PutSeccompIovec()

	c, err := findPID(int32(siov.msg.monitor_pid), s.s)
	if err != nil {
		if s.s.OS.SeccompListenerContinue {
			_ = siov.SendSeccompIovec(fd, 0, seccompUserNotifFlagContinue)
		} else {
			_ = siov.SendSeccompIovec(fd, int(-C.EPERM), 0)
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
	_ = os.Remove(s.path)
	return s.l.Close()
}

func lxcSupportSeccompNotifyContinue(state *state.State) error {
	err := lxcSupportSeccompNotify(state)
	if err != nil {
		return err
	}

	if !state.OS.SeccompListenerContinue {
		return fmt.Errorf("Seccomp notify doesn't support continuing syscalls")
	}

	return nil
}

func lxcSupportSeccompNotifyAddfd(state *state.State) error {
	err := lxcSupportSeccompNotify(state)
	if err != nil {
		return err
	}

	if !state.OS.SeccompListenerContinue {
		return fmt.Errorf("Seccomp notify doesn't support continuing syscalls")
	}

	if !state.OS.SeccompListenerAddfd {
		return fmt.Errorf("Seccomp notify doesn't support adding file descriptors")
	}

	return nil
}

func lxcSupportSeccompNotify(state *state.State) error {
	if !state.OS.SeccompListener {
		return fmt.Errorf("Seccomp notify not supported")
	}

	if !state.OS.LXCFeatures["seccomp_notify"] {
		return fmt.Errorf("LXC doesn't support seccomp notify")
	}

	c, err := liblxc.NewContainer("test-seccomp", state.OS.LxcPath)
	if err != nil {
		return fmt.Errorf("Failed to load seccomp notify test container")
	}

	err = c.SetConfigItem("lxc.seccomp.notify.proxy", fmt.Sprintf("unix:%s", shared.VarPath("seccomp.socket")))
	if err != nil {
		return fmt.Errorf("LXC doesn't support notify proxy: %w", err)
	}

	_ = c.Release()
	return nil
}

// MountSyscallFilter creates a mount syscall filter from the config.
func MountSyscallFilter(config map[string]string) []string {
	fs := []string{}

	if shared.IsFalseOrEmpty(config["security.syscalls.intercept.mount"]) {
		return fs
	}

	fsAllowed := strings.Split(config["security.syscalls.intercept.mount.allowed"], ",")
	if len(fsAllowed) > 0 && fsAllowed[0] != "" {
		fs = append(fs, fsAllowed...)
	}

	return fs
}

// SyscallInterceptMountFilter creates a new mount syscall interception filter
func SyscallInterceptMountFilter(config map[string]string) (map[string]string, error) {
	if shared.IsFalseOrEmpty(config["security.syscalls.intercept.mount"]) {
		return map[string]string{}, nil
	}

	fsMap := map[string]string{}
	fsFused := strings.Split(config["security.syscalls.intercept.mount.fuse"], ",")
	if len(fsFused) > 0 && fsFused[0] != "" {
		for _, ent := range fsFused {
			fsfuse := strings.Split(ent, "=")
			if len(fsfuse) != 2 {
				return map[string]string{}, fmt.Errorf("security.syscalls.intercept.mount.fuse is not of the form 'filesystem=fuse-binary': %s", ent)
			}

			// fsfuse[0] == filesystems that are ok to mount
			// fsfuse[1] == fuse binary to use to mount filesystemstype
			fsMap[fsfuse[0]] = fsfuse[1]
		}
	}

	fsAllowed := strings.Split(config["security.syscalls.intercept.mount.allowed"], ",")
	if len(fsAllowed) > 0 && fsAllowed[0] != "" {
		for _, allowedfs := range fsAllowed {
			if fsMap[allowedfs] != "" {
				return map[string]string{}, fmt.Errorf("Filesystem %s cannot appear in security.syscalls.intercept.mount.allowed and security.syscalls.intercept.mount.fuse", allowedfs)
			}

			fsMap[allowedfs] = ""
		}
	}

	return fsMap, nil
}

// MountSyscallValid checks whether this is a mount syscall we intercept.
func (s *Server) MountSyscallValid(c Instance, args *MountArgs) (bool, string) {
	fsMap, err := SyscallInterceptMountFilter(c.ExpandedConfig())
	if err != nil {
		return false, ""
	}

	fuse, ok := fsMap[args.fstype]
	if ok {
		return true, fuse
	}

	return false, ""
}

// MountSyscallShift checks whether this mount syscall needs shifting.
func (s *Server) MountSyscallShift(c Instance, path string) idmap.IdmapStorageType {
	if shared.IsTrue(c.ExpandedConfig()["security.syscalls.intercept.mount.shift"]) {
		diskIdmap, err := c.DiskIdmap()
		if err != nil {
			return idmap.IdmapStorageNone
		}

		if diskIdmap == nil {
			return c.IdmappedStorage(path, "none")
		}
	}

	return idmap.IdmapStorageNone
}

var pageSize = 4096

func init() {
	tmp := unix.Getpagesize()
	if tmp > 0 {
		pageSize = tmp
	}
}
