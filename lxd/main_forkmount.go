package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

/*
#define _GNU_SOURCE
#include <ctype.h>
#include <errno.h>
#include <fcntl.h>
#include <libgen.h>
#include <limits.h>
#include <lxc/lxccontainer.h>
#include <lxc/version.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#define VERSION_AT_LEAST(major, minor, micro)							\
	((LXC_DEVEL == 1) || (!(major > LXC_VERSION_MAJOR ||					\
	major == LXC_VERSION_MAJOR && minor > LXC_VERSION_MINOR ||				\
	major == LXC_VERSION_MAJOR && minor == LXC_VERSION_MINOR && micro > LXC_VERSION_MICRO)))

extern char* advance_arg(bool required);
extern void error(char *msg);
extern void attach_userns(int pid);
extern int dosetns(int pid, char *nstype);

int mkdir_p(const char *dir, mode_t mode)
{
	const char *tmp = dir;
	const char *orig = dir;
	char *makeme;

	do {
		dir = tmp + strspn(tmp, "/");
		tmp = dir + strcspn(dir, "/");
		makeme = strndup(orig, dir - orig);
		if (*makeme) {
			if (mkdir(makeme, mode) && errno != EEXIST) {
				fprintf(stderr, "failed to create directory '%s': %s\n", makeme, strerror(errno));
				free(makeme);
				return -1;
			}
		}
		free(makeme);
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

void ensure_file(char *dest) {
	struct stat sb;
	int fd;

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
	close(fd);
}

void create(char *src, char *dest) {
	char *dirdup;
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
		free(dirdup);
		_exit(1);
	}
	free(dirdup);

	switch (sb.st_mode & S_IFMT) {
	case S_IFDIR:
		ensure_dir(dest);
		return;
	default:
		ensure_file(dest);
		return;
	}
}

void do_lxd_forkmount(pid_t pid) {
	char *src, *dest, *opts;

	attach_userns(pid);

	if (dosetns(pid, "mnt") < 0) {
		fprintf(stderr, "Failed setns to container mount namespace: %s\n", strerror(errno));
		_exit(1);
	}

	src = advance_arg(true);
	dest = advance_arg(true);

	create(src, dest);

	if (access(src, F_OK) < 0) {
		fprintf(stderr, "Mount source doesn't exist: %s\n", strerror(errno));
		_exit(1);
	}

	if (access(dest, F_OK) < 0) {
		fprintf(stderr, "Mount destination doesn't exist: %s\n", strerror(errno));
		_exit(1);
	}

	// Here, we always move recursively, because we sometimes allow
	// recursive mounts. If the mount has no kids then it doesn't matter,
	// but if it does, we want to move those too.
	if (mount(src, dest, "none", MS_MOVE | MS_REC, NULL) < 0) {
		fprintf(stderr, "Failed mounting %s onto %s: %s\n", src, dest, strerror(errno));
		_exit(1);
	}

	_exit(0);
}

void do_lxd_forkumount(pid_t pid) {
	int ret;
	char *path = NULL;

	ret = dosetns(pid, "mnt");
	if (ret < 0) {
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

#if VERSION_AT_LEAST(3, 1, 0)
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
#endif

void do_lxc_forkmount()
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
#endif
}

void do_lxc_forkumount()
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
#endif
}

void forkmount() {
	char *cur = NULL;

	char *command = NULL;
	char *rootfs = NULL;
	pid_t pid = 0;

	// Get the subcommand
	command = advance_arg(false);
	if (command == NULL || (strcmp(command, "--help") == 0 || strcmp(command, "--version") == 0 || strcmp(command, "-h") == 0)) {
		return;
	}

	// Check that we're root
	if (geteuid() != 0) {
		fprintf(stderr, "Error: forkmount requires root privileges\n");
		_exit(1);
	}

	// Call the subcommands
	if (strcmp(command, "lxd-mount") == 0) {
		// Get the pid
		cur = advance_arg(false);
		if (cur == NULL || (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0)) {
			return;
		}
		pid = atoi(cur);

		do_lxd_forkmount(pid);
	} else if (strcmp(command, "lxc-mount") == 0) {
		do_lxc_forkmount();
	} else if (strcmp(command, "lxd-umount") == 0) {
		// Get the pid
		cur = advance_arg(false);
		if (cur == NULL || (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0)) {
			return;
		}
		pid = atoi(cur);

		do_lxd_forkumount(pid);
	} else if (strcmp(command, "lxc-umount") == 0) {
		do_lxc_forkumount();
	}
}
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
// #cgo LDFLAGS: -llxc
// #cgo pkg-config: lxc
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
	cmdMount := &cobra.Command{}
	cmdMount.Use = "mount <PID> <source> <destination>"
	cmdMount.Args = cobra.ExactArgs(3)
	cmdMount.RunE = c.Run
	cmd.AddCommand(cmdMount)

	// umount
	cmdUmount := &cobra.Command{}
	cmdUmount.Use = "umount <PID> <path>"
	cmdUmount.Args = cobra.ExactArgs(2)
	cmdUmount.RunE = c.Run
	cmd.AddCommand(cmdUmount)

	return cmd
}

func (c *cmdForkmount) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("This command should have been intercepted in cgo")
}
