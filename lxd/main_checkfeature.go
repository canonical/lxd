package main

import (
	"golang.org/x/sys/unix"
	"os"
	"runtime"
	// Used by cgo
	_ "github.com/lxc/lxd/lxd/include"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

/*
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <errno.h>
#include <fcntl.h>
#include <linux/types.h>
#include <poll.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <sched.h>
#include <signal.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>
#include <syscall.h>
#include <linux/seccomp.h>
#include <linux/filter.h>
#include <linux/audit.h>
#include <sys/ptrace.h>
#include <sys/wait.h>

#include "../shared/netutils/netns_getifaddrs.c"
#include "include/compiler.h"
#include "include/lxd_seccomp.h"
#include "include/memory_utils.h"
#include "include/process_utils.h"
#include "include/syscall_numbers.h"
#include "include/syscall_wrappers.h"

__ro_after_init bool close_range_aware = false;
__ro_after_init bool tiocgptpeer_aware = false;
__ro_after_init bool netnsid_aware = false;
__ro_after_init bool pidfd_aware = false;
__ro_after_init bool uevent_aware = false;
__ro_after_init int seccomp_notify_aware = 0;
__ro_after_init char errbuf[4096];

extern int can_inject_uevent(const char *uevent, size_t len);
extern int wait_for_pid(pid_t pid);
extern int preserve_ns(pid_t pid, int ns_fd, const char *ns);

static int netns_set_nsid(int fd)
{
	__do_close int sockfd = -EBADF;
	int ret;
	char buf[NLMSG_ALIGN(sizeof(struct nlmsghdr)) +
		 NLMSG_ALIGN(sizeof(struct rtgenmsg)) +
		 NLMSG_ALIGN(1024)];
	struct nlmsghdr *hdr;
	struct rtgenmsg *msg;
	__s32 ns_id = -1;
	__u32 netns_fd = fd;

	sockfd = netlink_open(NETLINK_ROUTE);
	if (sockfd < 0)
		return -1;

	memset(buf, 0, sizeof(buf));
	hdr = (struct nlmsghdr *)buf;
	msg = (struct rtgenmsg *)NLMSG_DATA(hdr);

	hdr->nlmsg_len = NLMSG_LENGTH(sizeof(*msg));
	hdr->nlmsg_type = RTM_NEWNSID;
	hdr->nlmsg_flags = NLM_F_REQUEST | NLM_F_ACK;
	hdr->nlmsg_pid = 0;
	hdr->nlmsg_seq = RTM_NEWNSID;
	msg->rtgen_family = AF_UNSPEC;

	addattr(hdr, 1024, __LXC_NETNSA_FD, &netns_fd, sizeof(netns_fd));
	addattr(hdr, 1024, __LXC_NETNSA_NSID, &ns_id, sizeof(ns_id));

	ret = netlink_transaction(sockfd, hdr, hdr);
	if (ret < 0)
		return -1;

	return 0;
}

void is_netnsid_aware(int *hostnetns_fd, int *newnetns_fd)
{
	__do_close int sock_fd = -EBADF;
	int netnsid, ret;

	*hostnetns_fd = open("/proc/self/ns/net", O_RDONLY | O_CLOEXEC);
	if (*hostnetns_fd < 0) {
		(void)sprintf(errbuf, "%s", "Failed to preserve host network namespace");
		return;
	}

	ret = unshare(CLONE_NEWNET);
	if (ret < 0) {
		(void)sprintf(errbuf, "%s", "Failed to unshare network namespace");
		return;
	}

	*newnetns_fd = open("/proc/self/ns/net", O_RDONLY | O_CLOEXEC);
	if (*newnetns_fd < 0) {
		(void)sprintf(errbuf, "%s", "Failed to preserve new network namespace");
		return;
	}

	ret = netns_set_nsid(*hostnetns_fd);
	if (ret < 0) {
		(void)sprintf(errbuf, "%s", "failed to set network namespace identifier");
		return;
	}

	netnsid = netns_get_nsid(*hostnetns_fd);
	if (netnsid < 0) {
		(void)sprintf(errbuf, "%s", "Failed to get network namespace identifier");
		return;
	}

	sock_fd = socket(PF_NETLINK, SOCK_RAW | SOCK_CLOEXEC, NETLINK_ROUTE);
	if (sock_fd < 0) {
		(void)sprintf(errbuf, "%s", "Failed to open netlink routing socket");
		return;
	}

	ret = setsockopt(sock_fd, SOL_NETLINK, NETLINK_GET_STRICT_CHK, &(int){1}, sizeof(int));
	if (ret < 0) {
		// NETLINK_GET_STRICT_CHK isn't supported
		return;
	}

	// NETLINK_GET_STRICT_CHK is supported
	netnsid_aware = true;
}

static void is_uevent_aware(void)
{
	if (can_inject_uevent("dummy", 6) < 0)
		return;

	uevent_aware = true;
}

#define ARRAY_SIZE(arr) (sizeof(arr) / sizeof((arr)[0]))

static int user_trap_syscall(int nr, unsigned int flags)
{
	struct sock_filter filter[] = {
		BPF_STMT(BPF_LD+BPF_W+BPF_ABS,
			offsetof(struct seccomp_data, nr)),
		BPF_JUMP(BPF_JMP+BPF_JEQ+BPF_K, nr, 0, 1),
		BPF_STMT(BPF_RET+BPF_K, SECCOMP_RET_USER_NOTIF),
		BPF_STMT(BPF_RET+BPF_K, SECCOMP_RET_ALLOW),
	};

	struct sock_fprog prog = {
		.len = (unsigned short)ARRAY_SIZE(filter),
		.filter = filter,
	};

	return syscall(__NR_seccomp, SECCOMP_SET_MODE_FILTER, flags, &prog);
}

// The ifdef can be safely ignored. We don't work on a kernel that old.
static int filecmp(pid_t pid1, pid_t pid2, int fd1, int fd2)
{
#ifdef __NR_kcmp
#ifndef KCMP_FILE
#define KCMP_FILE 0
#endif
	return syscall(__NR_kcmp, pid1, pid2, 0, fd1, fd2);
#else
	errno = ENOSYS;
	return -1;
#endif
}

__noreturn static void __do_user_notification_continue(void)
{
	__do_close int listener = -EBADF;
	pid_t pid;
	int ret;
	struct seccomp_notif req = {};
	struct seccomp_notif_resp resp = {};
	struct pollfd pollfd;

	listener = user_trap_syscall(__NR_dup, SECCOMP_FILTER_FLAG_NEW_LISTENER);
	if (listener < 0)
		_exit(EXIT_FAILURE);

	pid = fork();
	if (pid < 0)
		_exit(EXIT_FAILURE);

	if (pid == 0) {
		int dup_fd, pipe_fds[2];
		pid_t self;

		// Don't bother cleaning up. On child exit all of those
		// will be closed anyway.
		ret = pipe(pipe_fds);
		if (ret < 0)
			_exit(EXIT_FAILURE);

		// O_CLOEXEC doesn't matter as we're in the child and we're
		// not going to exec.
		dup_fd = dup(pipe_fds[0]);
		if (dup_fd < 0)
			_exit(EXIT_FAILURE);

		self = getpid();

		ret = filecmp(self, self, pipe_fds[0], dup_fd);
		if (ret)
			_exit(EXIT_FAILURE);

		_exit(EXIT_SUCCESS);
	}

	pollfd.fd = listener;
	pollfd.events = POLLIN | POLLOUT;

	ret = poll(&pollfd, 1, 5000);
	if (ret <= 0)
		goto cleanup_sigkill;

	if (!(pollfd.revents & POLLIN))
		goto cleanup_sigkill;

	ret = ioctl(listener, SECCOMP_IOCTL_NOTIF_RECV, &req);
	if (ret)
		goto cleanup_sigkill;

	pollfd.fd = listener;
	pollfd.events = POLLIN | POLLOUT;

	ret = poll(&pollfd, 1, 5000);
	if (ret <= 0)
		goto cleanup_sigkill;

	if (!(pollfd.revents & POLLOUT))
		goto cleanup_sigkill;

	if (req.data.nr != __NR_dup)
		goto cleanup_sigkill;

	resp.id = req.id;
	resp.flags |= SECCOMP_USER_NOTIF_FLAG_CONTINUE;
	ret = ioctl(listener, SECCOMP_IOCTL_NOTIF_SEND, &resp);
	resp.error = -EPERM;
	resp.flags = 0;
	if (ret) {
		ioctl(listener, SECCOMP_IOCTL_NOTIF_SEND, &resp);
		goto cleanup_sigkill;
	}

cleanup_wait:
	ret = wait_for_pid(pid);
	if (ret)
		_exit(EXIT_FAILURE);
	_exit(EXIT_SUCCESS);

cleanup_sigkill:
	kill(pid, SIGKILL);
	goto cleanup_wait;
}

static void is_user_notification_continue_aware(void)
{
	int ret;
	pid_t pid;

	pid = fork();
	if (pid < 0)
		return;

	if (pid == 0) {
		__do_user_notification_continue();
		// Should not be reached.
		_exit(EXIT_FAILURE);
	}

	ret = wait_for_pid(pid);
	if (!ret)
		seccomp_notify_aware = 2;
}

__noreturn static void __do_user_notification_addfd(void)
{
	__do_close int listener = -EBADF;
	pid_t pid;
	int ret;
	struct seccomp_notif req = {};
	struct seccomp_notif_resp resp = {};
	struct seccomp_notif_addfd addfd = {};
	struct pollfd pollfd;

	listener = user_trap_syscall(__NR_dup, SECCOMP_FILTER_FLAG_NEW_LISTENER);
	if (listener < 0)
		_exit(EXIT_FAILURE);

	pid = fork();
	if (pid < 0)
		_exit(EXIT_FAILURE);

	if (pid == 0) {
		int dup_fd, pipe_fds[2];
		pid_t self;

		// Don't bother cleaning up. On child exit all of those
		// will be closed anyway.
		ret = pipe(pipe_fds);
		if (ret < 0)
			_exit(EXIT_FAILURE);

		// O_CLOEXEC doesn't matter as we're in the child and we're
		// not going to exec.
		dup_fd = dup(pipe_fds[0]);
		if (dup_fd < 0)
			_exit(EXIT_FAILURE);

		self = getpid();

		ret = filecmp(self, self, pipe_fds[0], dup_fd);
		if (ret)
			_exit(EXIT_FAILURE);

		_exit(EXIT_SUCCESS);
	}

	pollfd.fd = listener;
	pollfd.events = POLLIN | POLLOUT;

	ret = poll(&pollfd, 1, 5000);
	if (ret <= 0)
		goto cleanup_sigkill;

	if (!(pollfd.revents & POLLIN))
		goto cleanup_sigkill;

	ret = ioctl(listener, SECCOMP_IOCTL_NOTIF_RECV, &req);
	if (ret)
		goto cleanup_sigkill;

	pollfd.fd = listener;
	pollfd.events = POLLIN | POLLOUT;

	ret = poll(&pollfd, 1, 5000);
	if (ret <= 0)
		goto cleanup_sigkill;

	if (!(pollfd.revents & POLLOUT))
		goto cleanup_sigkill;

	if (req.data.nr != __NR_dup)
		goto cleanup_sigkill;

	addfd.srcfd	= 3;
	addfd.id 	= req.id;
	addfd.flags 	= 0;

	// Inject the fd into the task.
	ret = ioctl(listener, SECCOMP_IOCTL_NOTIF_ADDFD, &addfd);
	if (ret < 0)
		goto cleanup_sigkill;
	close(ret);

	resp.id = req.id;
	resp.flags |= SECCOMP_USER_NOTIF_FLAG_CONTINUE;
	ret = ioctl(listener, SECCOMP_IOCTL_NOTIF_SEND, &resp);
	resp.error = -EPERM;
	resp.flags = 0;
	if (ret) {
		ioctl(listener, SECCOMP_IOCTL_NOTIF_SEND, &resp);
		goto cleanup_sigkill;
	}

cleanup_wait:
	ret = wait_for_pid(pid);
	if (ret)
		_exit(EXIT_FAILURE);
	_exit(EXIT_SUCCESS);

cleanup_sigkill:
	kill(pid, SIGKILL);
	goto cleanup_wait;
}

static void is_user_notification_addfd_aware(void)
{
	int ret;
	pid_t pid;

	pid = fork();
	if (pid < 0)
		return;

	if (pid == 0) {
		__do_user_notification_addfd();
		// Should not be reached.
		_exit(EXIT_FAILURE);
	}

	ret = wait_for_pid(pid);
	if (!ret)
		seccomp_notify_aware = 3;
}

static void is_seccomp_notify_aware(void)
{
	__u32 action[] = { SECCOMP_RET_USER_NOTIF };

	if (syscall(__NR_seccomp, SECCOMP_GET_ACTION_AVAIL, 0, &action[0]) == 0) {
		seccomp_notify_aware = 1;
		is_user_notification_continue_aware();
		if (seccomp_notify_aware == 2)
			is_user_notification_addfd_aware();
	}

}

static void is_pidfd_aware(void)
{
	__do_close int pidfd = -EBADF;
	int ret;

	pidfd = pidfd_open(getpid(), 0);
	if (pidfd < 0)
		return;

	// We don't care whether or not children were in a waitable state. We
	// just care whether waitid() recognizes P_PIDFD.
	ret = waitid(P_PIDFD, pidfd, NULL,
		    // Type of children to wait for.
		    __WALL |
		    // How to wait for them.
		    WNOHANG | WNOWAIT |
		    // What state to wait for.
		    WEXITED | WSTOPPED | WCONTINUED);
	if (ret < 0 && errno != ECHILD)
		return;

	ret = pidfd_send_signal(pidfd, 0, NULL, 0);
	if (ret)
		return;

	pidfd_aware = true;
}

#ifndef TIOCGPTPEER
	#if defined __sparc__
		#define TIOCGPTPEER _IO('t', 137)
	#else
		#define TIOCGPTPEER _IO('T', 0x41)
	#endif
#endif

static void is_tiocgptpeer_aware(void)
{
	__do_close int ptx_fd = -EBADF, pty_fd = -EBADF;
	int ret;

	ptx_fd = open("/dev/ptmx", O_RDWR | O_NOCTTY | O_CLOEXEC);
	if (ptx_fd < 0)
		return;

	ret = grantpt(ptx_fd);
	if (ret < 0)
		return;

	ret = unlockpt(ptx_fd);
	if (ret < 0)
		return;

	pty_fd = ioctl(ptx_fd, TIOCGPTPEER, O_RDWR | O_NOCTTY | O_CLOEXEC);
	if (pty_fd < 0)
		return;

	tiocgptpeer_aware = true;
}

static void is_close_range_aware(void)
{
	int fd;

	fd = open("/dev/null", O_RDONLY | O_CLOEXEC);
	if (fd < 0)
		return;

	if (close_range(fd, fd, CLOSE_RANGE_UNSHARE))
		return;

	close_range_aware = true;
}

void checkfeature(void)
{
	__do_close int hostnetns_fd = -EBADF, newnetns_fd = -EBADF;

	is_netnsid_aware(&hostnetns_fd, &newnetns_fd);
	is_pidfd_aware();
	is_uevent_aware();
	is_seccomp_notify_aware();
	is_tiocgptpeer_aware();
	is_close_range_aware();

	if (setns(hostnetns_fd, CLONE_NEWNET) < 0)
		(void)sprintf(errbuf, "%s", "Failed to attach to host network namespace");

}

static bool is_empty_string(char *s)
{
	return (errbuf[0] == '\0');
}
*/
import "C"

func canUseNetnsGetifaddrs() bool {
	if !bool(C.is_empty_string(&C.errbuf[0])) {
		logger.Debugf("%s", C.GoString(&C.errbuf[0]))
	}

	return bool(C.netnsid_aware)
}

func canUseUeventInjection() bool {
	return bool(C.uevent_aware)
}

func canUseSeccompListener() bool {
	return bool(C.seccomp_notify_aware > 0)
}

func canUseSeccompListenerContinue() bool {
	return bool(C.seccomp_notify_aware >= 2)
}

func canUseSeccompListenerAddfd() bool {
	return bool(C.seccomp_notify_aware == 3)
}
func canUsePidFds() bool {
	return bool(C.pidfd_aware)
}

func canUseShiftfs() bool {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hostMntNs, err := os.Open("/proc/self/ns/mnt")
	if err != nil {
		logger.Debugf("%s - Failed to open host mount namespace", err)
		return false
	}
	defer hostMntNs.Close()

	err = unix.Unshare(unix.CLONE_NEWNS)
	if err != nil {
		logger.Debugf("%s - Failed to unshare mount namespace", err)
		return false
	}
	defer func() {
		err = unix.Setns(int(hostMntNs.Fd()), unix.CLONE_NEWNS)
		if err != nil {
			logger.Debugf("%s - Failed to reattach to host mount namespace", err)
		}
	}()

	err = unix.Mount("/", "/", "", unix.MS_REC|unix.MS_PRIVATE, "")
	if err != nil {
		logger.Debugf("%s - Failed to turn \"/\" into private mount", err)
		return false
	}

	err = unix.Mount(shared.VarPath(), shared.VarPath(), "shiftfs", 0, "mark")
	if err != nil {
		logger.Debugf("%s - Failed to mount shiftfs", err)
		return false
	}

	return true
}

func canUseNativeTerminals() bool {
	return bool(C.tiocgptpeer_aware)
}

func canUseCloseRange() bool {
	return bool(C.close_range_aware)
}
