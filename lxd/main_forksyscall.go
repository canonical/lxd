package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

/*
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <fcntl.h>
#include <libgen.h>
#include <sched.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/capability.h>
#include <sys/fsuid.h>
#include <sys/prctl.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/vfs.h>
#include <sys/xattr.h>
#include <unistd.h>

#include "include/memory_utils.h"

extern char* advance_arg(bool required);
extern int dosetns(int pid, char *nstype);

static inline bool same_fsinfo(struct stat *s1, struct stat *s2,
			       struct statfs *sfs1, struct statfs *sfs2)
{
	return ((sfs1->f_type == sfs2->f_type) && (s1->st_dev == s2->st_dev));
}

static int fstat_fstatfs(int fd, struct stat *s, struct statfs *sfs)
{
	if (fstat(fd, s))
		return -1;

	if (fstatfs(fd, sfs))
		return -1;

	return 0;
}

static bool chdirchroot_in_mntns(int cwd_fd, int root_fd)
{
	ssize_t len;
	char buf[PATH_MAX];

	if (fchdir(cwd_fd))
		return false;

	len = readlinkat(root_fd, "", buf, sizeof(buf));
	if (len < 0 || len >= sizeof(buf))
		return false;
	buf[len] = '\0';

	if (chroot(buf))
		return false;

	return true;
}

// Expects command line to be in the form:
// <PID> <root-uid> <root-gid> <path> <mode> <dev>
static void forkmknod()
{
	__do_close_prot_errno int cwd_fd = -EBADF, host_target_fd = -EBADF, mnt_fd = -EBADF, root_fd = -EBADF, target_dir_fd = -EBADF;
	char *cur = NULL, *target = NULL, *target_dir = NULL, *target_host = NULL;
	int ret;
	char path[PATH_MAX];
	mode_t mode;
	dev_t dev;
	pid_t pid;
	uid_t fsuid, uid;
	gid_t fsgid, gid;
	struct stat s1, s2;
	struct statfs sfs1, sfs2;
	cap_t caps;

	pid = atoi(advance_arg(true));
	target = advance_arg(true);
	mode = atoi(advance_arg(true));
	dev = atoi(advance_arg(true));
	target_host = advance_arg(true);
	uid = atoi(advance_arg(true));
	gid = atoi(advance_arg(true));
	fsuid = atoi(advance_arg(true));
	fsgid = atoi(advance_arg(true));

	host_target_fd = open(dirname(target_host), O_PATH | O_RDONLY | O_CLOEXEC | O_DIRECTORY);
	if (host_target_fd < 0) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	snprintf(path, sizeof(path), "/proc/%d/ns/mnt", pid);
	mnt_fd = open(path, O_RDONLY | O_CLOEXEC);
	if (mnt_fd < 0) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	snprintf(path, sizeof(path), "/proc/%d/root", pid);
	root_fd = open(path, O_PATH | O_RDONLY | O_CLOEXEC | O_NOFOLLOW);
	if (root_fd < 0) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	snprintf(path, sizeof(path), "/proc/%d/cwd", pid);
	cwd_fd = open(path, O_PATH | O_RDONLY | O_CLOEXEC);
	if (cwd_fd < 0) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	if (setns(mnt_fd, CLONE_NEWNS)) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	if (!chdirchroot_in_mntns(cwd_fd, root_fd)) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	snprintf(path, sizeof(path), "%s", target);
	target_dir = dirname(path);
	target_dir_fd = open(target_dir, O_PATH | O_RDONLY | O_CLOEXEC | O_DIRECTORY);
	if (target_dir_fd < 0) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	caps = cap_get_pid(pid);
	if (!caps) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	ret = prctl(PR_SET_KEEPCAPS, 1);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	ret = setegid(gid);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	setfsgid(fsgid);

	ret = seteuid(uid);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	setfsuid(fsuid);

	ret = cap_set_proc(caps);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	ret = fstat_fstatfs(target_dir_fd, &s2, &sfs2);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	if (sfs2.f_flags & MS_NODEV) {
		fprintf(stderr, "%d", EPERM);
		_exit(EXIT_FAILURE);
	}

	ret = fstat_fstatfs(host_target_fd, &s1, &sfs1);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	if (!same_fsinfo(&s1, &s2, &sfs1, &sfs2)) {
		fprintf(stderr, "%d", ENOMEDIUM);
		_exit(EXIT_FAILURE);
	}

	// basename() can modify its argument so accessing target_host is
	// invalid from now on.
	ret = mknodat(target_dir_fd, target, mode, dev);
	if (ret) {
		if (errno == EPERM)
			fprintf(stderr, "%d", ENOMEDIUM);
		else
			fprintf(stderr, "%d", errno);

		_exit(EXIT_FAILURE);
	}
}

#ifndef CLONE_NEWCGROUP
#define CLONE_NEWCGROUP	0x02000000
#endif

const char *ns_names[] = { "user", "pid", "uts", "ipc", "net", "cgroup", NULL };

static bool setnsat(int ns_fd, const char *ns)
{
	__do_close_prot_errno int fd = -EBADF;

	fd = openat(ns_fd, ns, O_RDONLY | O_CLOEXEC);
	if (fd < 0)
		return false;

	return setns(fd, 0) == 0;
}

static bool change_creds(int ns_fd, cap_t caps, uid_t nsuid, gid_t nsgid, uid_t nsfsuid, gid_t nsfsgid)
{
	__do_close_prot_errno int fd = -EBADF;

	if (prctl(PR_SET_KEEPCAPS, 1))
		return false;

	for (const char **ns = ns_names; ns && *ns; ns++) {
		if (!setnsat(ns_fd, *ns))
			return false;
	}

	if (setegid(nsgid))
		return false;

	setfsgid(nsfsgid);

	if (seteuid(nsuid))
		return false;

	setfsuid(nsfsuid);

	if (cap_set_proc(caps))
		return false;

	return true;
}

static void forksetxattr()
{
	__do_close_prot_errno int cwd_fd = -EBADF, mnt_fd = -EBADF, ns_fd = -EBADF, root_fd = -EBADF, target_fd = -EBADF;
	int flags = 0;
	char *name, *target;
	char path[PATH_MAX];
	uid_t nsfsuid, nsuid;
	gid_t nsfsgid, nsgid;
	pid_t pid = 0;
	cap_t caps;
	cap_flag_value_t flag;
	int whiteout;
	void *data;
	size_t size;

	pid = atoi(advance_arg(true));
	nsuid = atoi(advance_arg(true));
	nsgid = atoi(advance_arg(true));
	nsfsuid = atoi(advance_arg(true));
	nsfsgid = atoi(advance_arg(true));
	name = advance_arg(true);
	target = advance_arg(true);
	flags = atoi(advance_arg(true));
	whiteout = atoi(advance_arg(true));
	size = atoi(advance_arg(true));
	data = advance_arg(true);

	snprintf(path, sizeof(path), "/proc/%d/ns", pid);
	ns_fd = open(path, O_PATH | O_RDONLY | O_CLOEXEC | O_DIRECTORY);
	if (ns_fd < 0) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	snprintf(path, sizeof(path), "/proc/%d/root", pid);
	root_fd = open(path, O_PATH | O_RDONLY | O_CLOEXEC | O_NOFOLLOW);
	if (root_fd < 0) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	snprintf(path, sizeof(path), "/proc/%d/ns/mnt", pid);
	mnt_fd = open(path, O_RDONLY | O_CLOEXEC);
	if (mnt_fd < 0) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	if (setns(mnt_fd, CLONE_NEWNS)) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	if (!chdirchroot_in_mntns(cwd_fd, root_fd)) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	target_fd = open(target, O_RDONLY | O_CLOEXEC);
	if (target_fd < 0) {
		fprintf(stderr, "%d", errno);
		_exit(EXIT_FAILURE);
	}

	caps = cap_get_pid(pid);
	if (!caps) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	if (whiteout == 1) {
		if (cap_get_flag(caps,  CAP_SYS_ADMIN, CAP_EFFECTIVE, &flag) != 0) {
			fprintf(stderr, "%d", EPERM);
			_exit(EXIT_FAILURE);
		}

		if (flag == CAP_CLEAR) {
			fprintf(stderr, "%d", EPERM);
			_exit(EXIT_FAILURE);
		}
	}

	if (whiteout == 1) {
		if (fsetxattr(target_fd, "trusted.overlay.opaque", "y", 1, flags)) {
			fprintf(stderr, "%d", errno);
			_exit(EXIT_FAILURE);
		}
	} else {
		if (!change_creds(ns_fd, caps, nsuid, nsgid, nsfsuid, nsfsgid)) {
			fprintf(stderr, "%d", EFAULT);
			_exit(EXIT_FAILURE);
		}

		if (fsetxattr(target_fd, name, data, size, flags)) {
			fprintf(stderr, "%d", errno);
			_exit(EXIT_FAILURE);
		}
	}
}

void forksyscall()
{
	char *syscall = NULL;

	// Check that we're root
	if (geteuid() != 0)
		_exit(EXIT_FAILURE);

	// Get the subcommand
	syscall = advance_arg(false);
	if (syscall == NULL ||
	    (strcmp(syscall, "--help") == 0 ||
	     strcmp(syscall, "--version") == 0 || strcmp(syscall, "-h") == 0))
		_exit(EXIT_SUCCESS);

	if (strcmp(syscall, "mknod") == 0)
		forkmknod();
	else if (strcmp(syscall, "setxattr") == 0)
		forksetxattr();
	else
		_exit(EXIT_FAILURE);

	_exit(EXIT_SUCCESS);
}
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
// #cgo LDFLAGS: -lcap
import "C"

type cmdForksyscall struct {
	global *cmdGlobal
}

func (c *cmdForksyscall) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forksyscall <syscall> <PID> <path> <mode> <dev>"
	cmd.Short = "Perform syscall operations"
	cmd.Long = `Description:
  Perform syscall operations

  This set of internal commands are used for all seccom-based container syscall
  operations.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForksyscall) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("This command should have been intercepted in cgo")
}
