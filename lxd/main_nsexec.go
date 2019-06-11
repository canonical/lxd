/**
 * This file is a bit funny. The goal here is to use setns() to manipulate
 * files inside the container, so we don't have to reason about the paths to
 * make sure they don't escape (we can simply rely on the kernel for
 * correctness). Unfortunately, you can't setns() to a mount namespace with a
 * multi-threaded program, which every golang binary is. However, by declaring
 * our init as an initializer, we can capture process control before it is
 * transferred to the golang runtime, so we can then setns() as we'd like
 * before golang has a chance to set up any threads. So, we implement two new
 * lxd fork* commands which are captured here, and take a file on the host fs
 * and copy it into the container ns.
 *
 * An alternative to this would be to move this code into a separate binary,
 * which of course has problems of its own when it comes to packaging (how do
 * we find the binary, what do we do if someone does file push and it is
 * missing, etc.). After some discussion, even though the embedded method is
 * somewhat convoluted, it was preferred.
 */
package main

/*
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <grp.h>
#include <linux/limits.h>
#include <sched.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#include "include/memory_utils.h"

// External functions
extern void checkfeature();
extern void forkfile();
extern void forkmknod();
extern void forkmount();
extern void forknet();
extern void forkdns();
extern void forkproxy();
extern void forkuevent();

// Command line parsing and tracking
char *cmdline_buf = NULL;
char *cmdline_cur = NULL;
ssize_t cmdline_size = -1;

char* advance_arg(bool required) {
	while (*cmdline_cur != 0)
		cmdline_cur++;

	cmdline_cur++;
	if (cmdline_size <= cmdline_cur - cmdline_buf) {
		if (!required)
			return NULL;

		fprintf(stderr, "not enough arguments\n");
		_exit(1);
	}

	return cmdline_cur;
}

void error(char *msg)
{
	int old_errno = errno;

	if (old_errno == 0) {
		fprintf(stderr, "%s\n", msg);
		fprintf(stderr, "errno: 0\n");
		return;
	}

	perror(msg);
	fprintf(stderr, "errno: %d\n", old_errno);
}

int dosetns(int pid, char *nstype) {
	__do_close_prot_errno int mntns = -EBADF;
	char buf[PATH_MAX];

	sprintf(buf, "/proc/%d/ns/%s", pid, nstype);
	mntns = open(buf, O_RDONLY);
	if (mntns < 0) {
		error("error: open mntns");
		return -1;
	}

	if (setns(mntns, 0) < 0) {
		error("error: setns");
		return -1;
	}

	return 0;
}

static int preserve_ns(const int pid, const char *ns)
{
	int ret;
// 5 /proc + 21 /int_as_str + 3 /ns + 20 /NS_NAME + 1 \0
#define __NS_PATH_LEN 50
	char path[__NS_PATH_LEN];

	// This way we can use this function to also check whether namespaces
	// are supported by the kernel by passing in the NULL or the empty
	// string.
	ret = snprintf(path, __NS_PATH_LEN, "/proc/%d/ns%s%s", pid,
		       !ns || strcmp(ns, "") == 0 ? "" : "/",
		       !ns || strcmp(ns, "") == 0 ? "" : ns);
	errno = EFBIG;
	if (ret < 0 || (size_t)ret >= __NS_PATH_LEN)
		return -EFBIG;

	return open(path, O_RDONLY | O_CLOEXEC);
}

// in_same_namespace - Check whether two processes are in the same namespace.
// @pid1 - PID of the first process.
// @pid2 - PID of the second process.
// @ns   - Name of the namespace to check. Must correspond to one of the names
//         for the namespaces as shown in /proc/<pid/ns/
//
// If the two processes are not in the same namespace returns an fd to the
// namespace of the second process identified by @pid2. If the two processes are
// in the same namespace returns -EINVAL, -1 if an error occurred.
static int in_same_namespace(pid_t pid1, pid_t pid2, const char *ns)
{
	__do_close_prot_errno int ns_fd1 = -1, ns_fd2 = -1;
	int ret = -1;
	struct stat ns_st1, ns_st2;

	ns_fd1 = preserve_ns(pid1, ns);
	if (ns_fd1 < 0) {
		// The kernel does not support this namespace. This is not an
		// error.
		if (errno == ENOENT)
			return -EINVAL;

		return -1;
	}

	ns_fd2 = preserve_ns(pid2, ns);
	if (ns_fd2 < 0)
		return -1;

	ret = fstat(ns_fd1, &ns_st1);
	if (ret < 0)
		return -1;

	ret = fstat(ns_fd2, &ns_st2);
	if (ret < 0)
		return -1;

	// processes are in the same namespace
	if ((ns_st1.st_dev == ns_st2.st_dev ) && (ns_st1.st_ino == ns_st2.st_ino))
		return -EINVAL;

	// processes are in different namespaces
	return move_fd(ns_fd2);
}

void attach_userns(int pid) {
	__do_close_prot_errno int userns_fd = -EBADF;
	int ret;

	userns_fd = in_same_namespace(getpid(), pid, "user");
	if (userns_fd < 0) {
		if (userns_fd == -EINVAL)
			return;

		_exit(EXIT_FAILURE);
	}

	ret = setns(userns_fd, CLONE_NEWUSER);
	if (ret < 0) {
		fprintf(stderr, "Failed setns to container user namespace: %s\n", strerror(errno));
		_exit(EXIT_FAILURE);
	}

	ret = setuid(0);
	if (ret < 0) {
		fprintf(stderr, "Failed setuid to container root user: %s\n", strerror(errno));
		_exit(1);
	}

	ret = setgid(0);
	if (ret < 0) {
		fprintf(stderr, "Failed setgid to container root group: %s\n", strerror(errno));
		_exit(1);
	}

	ret = setgroups(0, NULL);
	if (ret < 0) {
		fprintf(stderr, "Failed setgroups to container root groups: %s\n", strerror(errno));
		_exit(1);
	}
}

static ssize_t lxc_read_nointr(int fd, void *buf, size_t count)
{
	ssize_t ret;
again:
	ret = read(fd, buf, count);
	if (ret < 0 && errno == EINTR)
		goto again;

	return ret;
}

static char *file_to_buf(char *path, ssize_t *length)
{
	__do_close_prot_errno int fd = -EBADF;
	__do_free char *copy = NULL;
	char buf[PATH_MAX];

	if (!length)
		return NULL;

	fd = open(path, O_RDONLY | O_CLOEXEC);
	if (fd < 0)
		return NULL;

	*length = 0;
	for (;;) {
		int n;
		char *old = copy;

		n = lxc_read_nointr(fd, buf, sizeof(buf));
		if (n < 0)
			return NULL;
		if (!n)
			break;

		copy = realloc(old, (*length + n) * sizeof(*old));
		if (!copy)
			return NULL;

		memcpy(copy + *length, buf, n);
		*length += n;
	}

	return move_ptr(copy);
}

__attribute__((constructor)) void init(void) {
	__do_free char *cmdline = NULL;

	cmdline_buf = file_to_buf("/proc/self/cmdline", &cmdline_size);
	if (!cmdline_buf)
		_exit(232);

	// Skip the first argument (but don't fail on missing second argument)
	cmdline = cmdline_cur = cmdline_buf;
	while (*cmdline_cur != 0)
		cmdline_cur++;
	cmdline_cur++;
	if (cmdline_size <= cmdline_cur - cmdline_buf) {
		checkfeature();
		return;
	}

	// Intercepts some subcommands
	if (strcmp(cmdline_cur, "forkfile") == 0)
		forkfile();
	else if (strcmp(cmdline_cur, "forkmknod") == 0)
		forkmknod();
	else if (strcmp(cmdline_cur, "forkmount") == 0)
		forkmount();
	else if (strcmp(cmdline_cur, "forknet") == 0)
		forknet();
	else if (strcmp(cmdline_cur, "forkdns") == 0)
		forkdns();
	else if (strcmp(cmdline_cur, "forkproxy") == 0)
		forkproxy();
	else if (strcmp(cmdline_cur, "forkuevent") == 0)
		forkuevent();
	else if (strncmp(cmdline_cur, "-", 1) == 0 || strcmp(cmdline_cur, "daemon") == 0)
		checkfeature();
}
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
import "C"
