package main

import (
	"fmt"

	"github.com/spf13/cobra"

	// Used by cgo
	_ "github.com/lxc/lxd/lxd/include"
)

/*
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <ctype.h>
#include <errno.h>
#include <fcntl.h>
#include <libgen.h>
#include <limits.h>
#include <lxc/lxccontainer.h>
#include <lxc/version.h>
#include <sched.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#include "include/memory_utils.h"
#include "include/mount_utils.h"
#include "include/syscall_numbers.h"
#include "include/syscall_wrappers.h"

#define VERSION_AT_LEAST(major, minor, micro)							\
	((LXC_DEVEL == 1) || (!(major > LXC_VERSION_MAJOR ||					\
	major == LXC_VERSION_MAJOR && minor > LXC_VERSION_MINOR ||				\
	major == LXC_VERSION_MAJOR && minor == LXC_VERSION_MINOR && micro > LXC_VERSION_MICRO)))

extern char* advance_arg(bool required);
extern void error(char *msg);
extern void attach_userns_fd(int ns_fd);
extern int pidfd_nsfd(int pidfd, pid_t pid);
extern int preserve_ns(pid_t pid, int ns_fd, const char *ns);
extern bool change_namespaces(int pidfd, int nsfd, unsigned int flags);
extern int mount_detach_idmap(const char *path, int fd_userns);

int mkdir_p(const char *dir, mode_t mode)
{
	const char *tmp = dir;
	const char *orig = dir;

	do {
		__do_free char *makeme = NULL;

		dir = tmp + strspn(tmp, "/");
		tmp = dir + strcspn(dir, "/");
		makeme = strndup(orig, dir - orig);
		if (*makeme) {
			if (mkdir(makeme, mode) && errno != EEXIST) {
				fprintf(stderr, "failed to create directory '%s': %s\n", makeme, strerror(errno));
				return -1;
			}
		}
	} while(tmp != dir);

	return 0;
}

void ensure_dir(char *dest) {
	struct stat sb;
	if (stat(dest, &sb) == 0) {
		if ((sb.st_mode & S_IFMT) == S_IFDIR)
			return;
		if (unlink(dest) < 0) {
			fprintf(stderr, "Failed to remove old %s: %s\n", dest, strerror(errno));
			_exit(1);
		}
	}
	if (mkdir(dest, 0755) < 0) {
		fprintf(stderr, "Failed to mkdir %s: %s\n", dest, strerror(errno));
		_exit(1);
	}
}

void ensure_file(char *dest)
{
	__do_close int fd = -EBADF;
	struct stat sb;

	if (stat(dest, &sb) == 0) {
		if ((sb.st_mode & S_IFMT) != S_IFDIR)
			return;
		if (rmdir(dest) < 0) {
			fprintf(stderr, "Failed to remove old %s: %s\n", dest, strerror(errno));
			_exit(1);
		}
	}

	fd = creat(dest, 0755);
	if (fd < 0) {
		fprintf(stderr, "Failed to mkdir %s: %s\n", dest, strerror(errno));
		_exit(1);
	}
}

void create(char *src, char *dest)
{
	__do_free char *dirdup = NULL;
	char *destdirname;

	struct stat sb;
	if (stat(src, &sb) < 0) {
		fprintf(stderr, "source %s does not exist\n", src);
		_exit(1);
	}

	dirdup = strdup(dest);
	if (!dirdup)
		_exit(1);

	destdirname = dirname(dirdup);

	if (mkdir_p(destdirname, 0755) < 0) {
		fprintf(stderr, "failed to create path: %s\n", destdirname);
		_exit(1);
	}

	switch (sb.st_mode & S_IFMT) {
	case S_IFDIR:
		ensure_dir(dest);
		return;
	default:
		ensure_file(dest);
		return;
	}
}

static int lxc_safe_ulong(const char *numstr, unsigned long *converted)
{
	char *err = NULL;
	unsigned long int uli;

	while (isspace(*numstr))
		numstr++;

	if (*numstr == '-')
		return -EINVAL;

	errno = 0;
	uli = strtoul(numstr, &err, 0);
	if (errno == ERANGE && uli == ULONG_MAX)
		return -ERANGE;

	if (err == numstr || *err != '\0')
		return -EINVAL;

	*converted = uli;
	return 0;
}

static void do_lxd_forkmount(int pidfd, int ns_fd)
{
	unsigned long mntflags = 0;
	int fd_tree = -EBADF;
	int ret;
	char *src, *dest, *idmapType, *flags;

	src = advance_arg(true);
	dest = advance_arg(true);
	idmapType = advance_arg(true);
	flags = advance_arg(true);

	if (strcmp(idmapType, "idmapped") == 0) {
		int fd_mntns, fd_userns;

		fd_userns = preserve_ns(-ESRCH, ns_fd, "user");
		if (fd_userns < 0) {
			fprintf(stderr, "Failed to open user namespace of container: %s\n", strerror(errno));
			_exit(1);
		}

		fd_mntns = preserve_ns(getpid(), -EBADF, "mnt");
		if (fd_mntns < 0) {
			fprintf(stderr, "Failed to open mount namespace of container: %s\n", strerror(errno));
			_exit(1);
		}

		if (!change_namespaces(pidfd, ns_fd, CLONE_NEWNS)) {
			fprintf(stderr, "Failed setns to container mount namespace: %s\n", strerror(errno));
			_exit(1);
		}

		fd_tree = mount_detach_idmap(src, fd_userns);
		if (fd_tree < 0) {
			fprintf(stderr, "Failed to create detached idmapped mount \"%s\": %s\n", src, strerror(errno));
			_exit(1);
		}

		ret = setns(fd_mntns, CLONE_NEWNS);
		if (ret) {
			fprintf(stderr, "Failed to switch to original mount namespace: %s\n", strerror(errno));
			_exit(1);
		}

		close_prot_errno_disarm(fd_userns);
		close_prot_errno_disarm(fd_mntns);
	}

	attach_userns_fd(ns_fd);

	if (!change_namespaces(pidfd, ns_fd, CLONE_NEWNS)) {
		fprintf(stderr, "Failed setns to container mount namespace: %s\n", strerror(errno));
		_exit(1);
	}

	create(src, dest);

	if (access(src, F_OK) < 0) {
		fprintf(stderr, "Mount source doesn't exist: %s\n", strerror(errno));
		_exit(1);
	}

	if (access(dest, F_OK) < 0) {
		fprintf(stderr, "Mount destination doesn't exist: %s\n", strerror(errno));
		_exit(1);
	}

	if (fd_tree >= 0) {
		ret = move_mount(fd_tree, "", -EBADF, dest, MOVE_MOUNT_F_EMPTY_PATH);
		if (ret) {
			fprintf(stderr, "Failed to move detached mount to target from %d to %s: %s\n", fd_tree, dest, strerror(errno));
			_exit(1);
		}

		close_prot_errno_disarm(fd_tree);
		_exit(0);
	}

	ret = lxc_safe_ulong(flags, &mntflags);
	if (ret < 0)
		_exit(1);

	if (strcmp(idmapType, "shiftfs") == 0) {
		// Setup shiftfs inside the container
		if (mount(src, src, "shiftfs", mntflags, "passthrough=3") < 0) {
			fprintf(stderr, "Failed shiftfs setup for %s: %s\n", src, strerror(errno));
			_exit(1);
		}
	}

	// Here, we always move recursively, because we sometimes allow
	// recursive mounts. If the mount has no kids then it doesn't matter,
	// but if it does, we want to move those too.
	if (mount(src, dest, "none", MS_MOVE | MS_REC, NULL) < 0) {
		// If using shiftfs, undo the shiftfs mount
		if (strcmp(idmapType, "shiftfs") == 0) {
			umount2(src, MNT_DETACH);
		}

		fprintf(stderr, "Failed mounting %s onto %s: %s\n", src, dest, strerror(errno));
		_exit(1);
	}

	if (strcmp(idmapType, "shiftfs") == 0) {
		// Clear source mount as target is now in place
		if (umount2(src, MNT_DETACH) < 0) {
			fprintf(stderr, "Failed shiftfs source unmount for %s: %s\n", src, strerror(errno));
			_exit(1);
		}
	}

	_exit(0);
}

void do_lxd_forkumount(int pidfd, int ns_fd)
{
	int ret;
	char *path = NULL;

	if (!change_namespaces(pidfd, ns_fd, CLONE_NEWNS)) {
		fprintf(stderr, "Failed to setns to container mount namespace: %s\n", strerror(errno));
		_exit(1);
	}

	path = advance_arg(true);

	ret = umount2(path, MNT_DETACH);
	if (ret < 0) {
		// - ENOENT: The user must have unmounted and removed the path.
		// - EINVAL: The user must have unmounted. Other explanations
		//           for EINVAL do not apply.
		if (errno == ENOENT || errno == EINVAL)
			_exit(0);

		fprintf(stderr, "Error unmounting %s: %s\n", path, strerror(errno));
		_exit(1);
	}

	_exit(0);
}

static void do_lxc_forkmount(void)
{
#if VERSION_AT_LEAST(3, 1, 0)
	int ret;
	char *config, *flags, *fstype, *lxcpath, *name, *source, *target;
	struct lxc_container *c;
	struct lxc_mount mnt = {0};
	unsigned long mntflags = 0;

	name = advance_arg(true);
	lxcpath = advance_arg(true);
	config = advance_arg(true);
	source = advance_arg(true);
	target = advance_arg(true);
	fstype = advance_arg(true);
	flags = advance_arg(true);

	c = lxc_container_new(name, lxcpath);
	if (!c)
		_exit(1);

	c->clear_config(c);

	if (!c->load_config(c, config)) {
		lxc_container_put(c);
		_exit(1);
	}

	ret = lxc_safe_ulong(flags, &mntflags);
	if (ret < 0) {
		lxc_container_put(c);
		_exit(1);
	}

	ret = c->mount(c, source, target, fstype, mntflags, NULL, &mnt);
	lxc_container_put(c);
	if (ret < 0)
		_exit(1);

	_exit(0);
#else
	fprintf(stderr, "error: Called lxc_forkmount when missing LXC support\n");
	_exit(1);
#endif
}

static void do_lxc_forkumount(void)
{
#if VERSION_AT_LEAST(3, 1, 0)
	int ret;
	char *config, *lxcpath, *name, *target;
	struct lxc_container *c;
	struct lxc_mount mnt = {0};

	name = advance_arg(true);
	lxcpath = advance_arg(true);
	config = advance_arg(true);
	target = advance_arg(true);

	c = lxc_container_new(name, lxcpath);
	if (!c)
		_exit(1);

	c->clear_config(c);

	if (!c->load_config(c, config)) {
		lxc_container_put(c);
		_exit(1);
	}

	ret = c->umount(c, target, MNT_DETACH, &mnt);
	lxc_container_put(c);
	if (ret < 0)
		_exit(1);

	_exit(0);
#else
	fprintf(stderr, "error: Called lxc_forkumount when missing LXC support\n");
	_exit(1);
#endif
}

void forkmount(void)
{
	char *command = NULL, *cur = NULL;
	int ns_fd = -EBADF, pidfd = -EBADF;
	pid_t pid = 0;

	// Get the subcommand
	command = advance_arg(false);
	if (command == NULL || (strcmp(command, "--help") == 0 || strcmp(command, "--version") == 0 || strcmp(command, "-h") == 0))
		return;

	// Check that we're root
	if (geteuid() != 0) {
		fprintf(stderr, "Error: forkmount requires root privileges\n");
		_exit(1);
	}

	// skip "--"
	advance_arg(true);

	// Call the subcommands
	if (strcmp(command, "lxd-mount") == 0) {
		// Get the pid
		cur = advance_arg(false);
		if (cur == NULL || (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0))
			return;

		pid = atoi(cur);
		if (pid <= 0)
			_exit(EXIT_FAILURE);

		pidfd = atoi(advance_arg(true));
		ns_fd = pidfd_nsfd(pidfd, pid);
		if (ns_fd < 0)
			_exit(EXIT_FAILURE);

		do_lxd_forkmount(pidfd, ns_fd);
	} else if (strcmp(command, "lxc-mount") == 0) {
		do_lxc_forkmount();
	} else if (strcmp(command, "lxd-umount") == 0) {
		// Get the pid
		cur = advance_arg(false);
		if (cur == NULL || (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0))
			return;

		pid = atoi(cur);
		if (pid <= 0)
			_exit(EXIT_FAILURE);

		pidfd = atoi(advance_arg(true));
		ns_fd = pidfd_nsfd(pidfd, pid);
		if (ns_fd < 0)
			_exit(EXIT_FAILURE);

		do_lxd_forkumount(pidfd, ns_fd);
	} else if (strcmp(command, "lxc-umount") == 0) {
		do_lxc_forkumount();
	}
}
*/
import "C"

type cmdForkmount struct {
	global *cmdGlobal
}

func (c *cmdForkmount) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkmount"
	cmd.Short = "Perform mount operations"
	cmd.Long = `Description:
  Perform mount operations

  This set of internal commands are used for all container mount
  operations.
`
	cmd.Hidden = true

	// mount
	cmdLXCMount := &cobra.Command{}
	cmdLXCMount.Use = "lxc-mount <name> <lxcpath> <configpath> <source> <destination> <fstype> <flags>"
	cmdLXCMount.Args = cobra.ExactArgs(7)
	cmdLXCMount.RunE = c.Run
	cmd.AddCommand(cmdLXCMount)

	cmdLXDMount := &cobra.Command{}
	cmdLXDMount.Use = "lxd-mount <PID> <PidFd> <source> <destination> <idmapType> <flags>"
	cmdLXDMount.Args = cobra.ExactArgs(6)
	cmdLXDMount.RunE = c.Run
	cmd.AddCommand(cmdLXDMount)

	// umount
	cmdLXCUmount := &cobra.Command{}
	cmdLXCUmount.Use = "lxc-umount <name> <lxcpath> <configpath> <path>"
	cmdLXCUmount.Args = cobra.ExactArgs(4)
	cmdLXCUmount.RunE = c.Run
	cmd.AddCommand(cmdLXCUmount)

	cmdLXDUmount := &cobra.Command{}
	cmdLXDUmount.Use = "lxd-umount <PID> <PidFd> <path>"
	cmdLXDUmount.Args = cobra.ExactArgs(3)
	cmdLXDUmount.RunE = c.Run
	cmd.AddCommand(cmdLXDUmount)

	return cmd
}

func (c *cmdForkmount) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("This command should have been intercepted in cgo")
}
