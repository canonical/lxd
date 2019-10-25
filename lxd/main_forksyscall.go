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

extern void attach_userns(int pid);
extern char* advance_arg(bool required);
extern int dosetns(int pid, char *nstype);

static inline bool same_fsinfo(struct stat *s1, struct stat *s2,
			       struct statfs *sfs1, struct statfs *sfs2)
{
	return ((sfs1->f_type == sfs2->f_type) && (s1->st_dev == s2->st_dev));
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

static bool acquire_basic_creds(pid_t pid)
{
	__do_close_prot_errno int cwd_fd = -EBADF, mnt_fd = -EBADF, root_fd = -EBADF;
	char buf[256];

	snprintf(buf, sizeof(buf), "/proc/%d/ns/mnt", pid);
	mnt_fd = open(buf, O_RDONLY | O_CLOEXEC);
	if (mnt_fd < 0)
		return false;

	snprintf(buf, sizeof(buf), "/proc/%d/root", pid);
	root_fd = open(buf, O_PATH | O_RDONLY | O_CLOEXEC | O_NOFOLLOW);
	if (root_fd < 0)
		return false;

	snprintf(buf, sizeof(buf), "/proc/%d/cwd", pid);
	cwd_fd = open(buf, O_PATH | O_RDONLY | O_CLOEXEC);
	if (cwd_fd < 0)
		return false;

	if (setns(mnt_fd, CLONE_NEWNS))
		return false;

	return chdirchroot_in_mntns(cwd_fd, root_fd);
}

static bool acquire_final_creds(pid_t pid, uid_t uid, gid_t gid, uid_t fsuid, gid_t fsgid)
{
	int ret;
	cap_t caps;

	caps = cap_get_pid(pid);
	if (!caps) {
		fprintf(stderr, "%d", ENOANO);
		return false;
	}

	ret = prctl(PR_SET_KEEPCAPS, 1);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		return false;
	}

	ret = setegid(gid);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		return false;
	}

	setfsgid(fsgid);
	if (setfsgid(-1) != fsgid) {
		fprintf(stderr, "%d", ENOANO);
		return false;
	}


	ret = seteuid(uid);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		return false;
	}

	setfsuid(fsuid);
	if (setfsuid(-1) != fsuid) {
		fprintf(stderr, "%d", ENOANO);
		return false;
	}

	ret = cap_set_proc(caps);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		return false;
	}

	return true;
}

// Expects command line to be in the form:
// <PID> <root-uid> <root-gid> <path> <mode> <dev>
static void mknod_emulate(void)
{
	__do_close_prot_errno int target_dir_fd = -EBADF;
	char *target = NULL, *target_dir = NULL;
	int ret;
	char path[PATH_MAX];
	mode_t mode;
	dev_t dev;
	pid_t pid;
	uid_t fsuid, uid;
	gid_t fsgid, gid;
	struct statfs sfs;

	pid = atoi(advance_arg(true));
	target = advance_arg(true);
	mode = atoi(advance_arg(true));
	dev = atoi(advance_arg(true));
	advance_arg(true);
	uid = atoi(advance_arg(true));
	gid = atoi(advance_arg(true));
	fsuid = atoi(advance_arg(true));
	fsgid = atoi(advance_arg(true));

	if (!acquire_basic_creds(pid)) {
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

	if (!acquire_final_creds(pid, uid, gid, fsuid, fsgid))
		_exit(EXIT_FAILURE);

	ret = fstatfs(target_dir_fd, &sfs);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	if (sfs.f_flags & MS_NODEV) {
		fprintf(stderr, "%d", EPERM);
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

static void setxattr_emulate(void)
{
	__do_close_prot_errno int ns_fd = -EBADF, target_fd = -EBADF;
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

	if (!acquire_basic_creds(pid)) {
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

static bool is_dir(const char *path)
{
	struct stat statbuf;
	int ret;

	ret = stat(path, &statbuf);
	if (ret == 0 && S_ISDIR(statbuf.st_mode))
		return true;

	return false;
}

static int make_tmpfile(char *template, bool dir)
{
	__do_close_prot_errno int fd = -EBADF;

	if (dir) {
		if (!mkdtemp(template))
			return -1;

		return 0;
	}

	fd = mkstemp(template);
	if (fd < 0)
		return -1;

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
	if (ret < 0 || (size_t)ret >= __NS_PATH_LEN) {
		errno = EFBIG;
		return -1;
	}

	return open(path, O_RDONLY | O_CLOEXEC);
}

static void mount_emulate(void)
{
	__do_close_prot_errno int mnt_fd = -EBADF;
	char *source = NULL, *shiftfs = NULL, *target = NULL, *fstype = NULL;
	uid_t uid = -1, fsuid = -1;
	gid_t gid = -1, fsgid = -1;
	int ret;
	pid_t pid = -1;
	unsigned long flags = 0;
	const void *data;

	pid = atoi(advance_arg(true));
	source = advance_arg(true);
	target = advance_arg(true);
	fstype = advance_arg(true);
	flags = atoi(advance_arg(true));
	shiftfs = advance_arg(true);
	uid = atoi(advance_arg(true));
	gid = atoi(advance_arg(true));
	fsuid = atoi(advance_arg(true));
	fsgid = atoi(advance_arg(true));
	data = advance_arg(false);

	mnt_fd = preserve_ns(getpid(), "mnt");
	if (mnt_fd < 0)
		_exit(EXIT_FAILURE);

	if (!acquire_basic_creds(pid))
		_exit(EXIT_FAILURE);

	if (!acquire_final_creds(pid, uid, gid, fsuid, fsgid))
		_exit(EXIT_FAILURE);

	if (strcmp(shiftfs, "true") == 0) {
		char template[] = P_tmpdir "/.lxd_tmp_mount_XXXXXX";

		// Create basic mount in container's mount namespace.
		ret = mount(source, target, fstype, flags, data);
		if (ret)
			_exit(EXIT_FAILURE);

		// Mark the mount as shiftable.
		ret = mount(target, target, "shiftfs", 0, "mark,passthrough=3");
		if (ret) {
			umount2(target, MNT_DETACH);
			_exit(EXIT_FAILURE);
		}

		// We need to reattach to the old mount namespace, then attach
		// to the user namespace of the container, and then attach to
		// the mount namespace again to get the ownership right when
		// creating our final shiftfs mount.
		ret = setns(mnt_fd, CLONE_NEWNS);
		if (ret) {
			umount2(target, MNT_DETACH);
			umount2(target, MNT_DETACH);
			_exit(EXIT_FAILURE);
		}

		attach_userns(pid);
		if (!acquire_basic_creds(pid)) {
			umount2(target, MNT_DETACH);
			umount2(target, MNT_DETACH);
			_exit(EXIT_FAILURE);
		}

		if (!acquire_final_creds(pid, uid, gid, fsuid, fsgid)) {
			umount2(target, MNT_DETACH);
			umount2(target, MNT_DETACH);
			_exit(EXIT_FAILURE);
		}

		ret = mount(target, target, "shiftfs", 0, "passthrough=3");
		if (ret) {
			umount2(target, MNT_DETACH);
			umount2(target, MNT_DETACH);
			_exit(EXIT_FAILURE);
		}

		ret = make_tmpfile(template, is_dir(target));
		if (ret) {
			umount2(target, MNT_DETACH);
			umount2(target, MNT_DETACH);
			umount2(target, MNT_DETACH);
			_exit(EXIT_FAILURE);
		}

		ret = mount(target, template, "none", MS_MOVE | MS_REC, NULL);
		if (ret) {
			remove(template);
			umount2(target, MNT_DETACH);
			umount2(target, MNT_DETACH);
			umount2(target, MNT_DETACH);
			_exit(EXIT_FAILURE);
		}

		umount2(target, MNT_DETACH);
		umount2(target, MNT_DETACH);

		ret = mount(template, target, "none", MS_MOVE | MS_REC, NULL);
		if (ret) {
			umount2(template, MNT_DETACH);
			remove(template);
			_exit(EXIT_FAILURE);
		}
		remove(template);
	} else {
		if (mount(source, target, fstype, flags, data) < 0)
			_exit(EXIT_FAILURE);
	}
}

void forksyscall(void)
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
		mknod_emulate();
	else if (strcmp(syscall, "setxattr") == 0)
		setxattr_emulate();
	else if (strcmp(syscall, "mount") == 0)
		mount_emulate();
	else
		_exit(EXIT_FAILURE);

	_exit(EXIT_SUCCESS);
}
*/
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
