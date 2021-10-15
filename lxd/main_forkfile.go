package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

/*
#include "config.h"

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <sched.h>
#include <stdbool.h>
#include <stdio.h>
#include <signal.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>
#include <limits.h>

#include "lxd.h"
#include "memory_utils.h"

static int copy(int target, int source, bool append)
{
	ssize_t n;
	char buf[1024];

	if (!append && ftruncate(target, 0) < 0) {
		error("error: truncate");
		return -1;
	}

	if (append && lseek(target, 0, SEEK_END) < 0) {
		error("error: seek");
		return -1;
	}

	while ((n = read(source, buf, 1024)) > 0) {
		if (write(target, buf, n) != n) {
			error("error: write");
			return -1;
		}
	}

	if (n < 0) {
		error("error: read");
		return -1;
	}

	return 0;
}

struct file_args {
	char *rootfs;
	int pidfd;
	int ns_fd;
	char *host;
	char *container;
	bool is_put;
	char *type;
	uid_t uid;
	gid_t gid;
	mode_t mode;
	uid_t uid_default;
	gid_t gid_default;
	mode_t mode_default;
	bool append;
};

static int manip_file_in_ns(struct file_args *args)
{
	__do_close int host_fd = -EBADF, container_fd = -EBADF;
	char *rootfs = args->rootfs;
	int pidfd = args->pidfd;
	int ns_fd = args->ns_fd;
	char *host = args->host;
	char *container = args->container;
	bool is_put = args->is_put;
	char *type = args->type;
	uid_t uid = args->uid;
	gid_t gid = args->gid;
	mode_t mode = args->mode;
	uid_t defaultUid = args->uid_default;
	gid_t defaultGid = args->gid_default;
	mode_t defaultMode = args->mode_default;
	bool append = args->append;
	int exists = -1, fret = -1;
	int container_open_flags;
	struct stat st;
	bool is_dir_manip = type != NULL && !strcmp(type, "directory");
	bool is_symlink_manip = type != NULL && !strcmp(type, "symlink");
	char link_target[PATH_MAX];
	ssize_t link_length;

	if (!is_dir_manip && !is_symlink_manip) {
		if (is_put)
			host_fd = open(host, O_RDONLY);
		else
			host_fd = open(host, O_RDWR);
		if (host_fd < 0) {
			error("error: open");
			return -1;
		}
	}

	if (ns_fd >= 0) {
		attach_userns_fd(ns_fd);

		if (!change_namespaces(pidfd, ns_fd, CLONE_NEWNS)) {
			error("error: setns");
			return -1;
		}
	} else {
		if (chroot(rootfs) < 0) {
			error("error: chroot");
			return -1;
		}

		if (chdir("/") < 0) {
			error("error: chdir");
			return -1;
		}
	}

	if (is_put && is_dir_manip) {
		if (mode == -1) {
			mode = defaultMode;
		}

		if (uid == -1) {
			uid = defaultUid;
		}

		if (gid == -1) {
			gid = defaultGid;
		}

		if (mkdir(container, mode) < 0 && errno != EEXIST) {
			error("error: mkdir");
			return -1;
		}

		if (chown(container, uid, gid) < 0) {
			error("error: chown");
			return -1;
		}

		return 0;
	}

	if (is_put && is_symlink_manip) {
		if (mode == -1) {
			mode = defaultMode;
		}

		if (uid == -1) {
			uid = defaultUid;
		}

		if (gid == -1) {
			gid = defaultGid;
		}

		if (symlink(host, container) < 0 && errno != EEXIST) {
			error("error: symlink");
			return -1;
		}

		if (fchownat(0, container, uid, gid, AT_SYMLINK_NOFOLLOW) < 0) {
			error("error: chown");
			return -1;
		}

		return 0;
	}

	if (fstatat(AT_FDCWD, container, &st, AT_SYMLINK_NOFOLLOW) < 0)
		exists = 0;

	if (is_put)
		container_open_flags = O_RDWR | O_CREAT;
	else
		container_open_flags = O_RDONLY;

	if (is_put && !is_dir_manip && exists && S_ISDIR(st.st_mode)) {
		error("error: Path already exists as a directory");
		return -1;
	}

	if (exists && S_ISDIR(st.st_mode))
		container_open_flags = O_DIRECTORY;

	if (!is_put && exists && S_ISLNK(st.st_mode)) {
		fprintf(stderr, "uid: %ld\n", (long)st.st_uid);
		fprintf(stderr, "gid: %ld\n", (long)st.st_gid);
		fprintf(stderr, "mode: %ld\n", (unsigned long)st.st_mode & (S_IRWXU | S_IRWXG | S_IRWXO));
		fprintf(stderr, "type: symlink\n");

		link_length = readlink(container, link_target, PATH_MAX);
		if (link_length < 0 || link_length >= PATH_MAX) {
			error("error: readlink");
			return -1;
		}
		link_target[link_length] = '\0';

		dprintf(host_fd, "%s\n", link_target);
		return -1;
	}

	umask(0);
	container_fd = open(container, container_open_flags, 0);
	if (container_fd < 0) {
		error("error: open");
		return -1;
	}
	if (is_put) {
		if (!exists) {
			if (mode == -1) {
				mode = defaultMode;
			}

			if (uid == -1) {
				uid = defaultUid;
			}

			if (gid == -1) {
				gid = defaultGid;
			}
		}

		if (copy(container_fd, host_fd, append) < 0) {
			error("error: copy");
			return -1;
		}

		if (mode != -1 && fchmod(container_fd, mode) < 0) {
			error("error: chmod");
			return -1;
		}

		if (fchown(container_fd, uid, gid) < 0) {
			error("error: chown");
			return -1;
		}
		fret = 0;
	} else {
		if (fstat(container_fd, &st) < 0) {
			error("error: stat");
			return -1;
		}

		fprintf(stderr, "uid: %ld\n", (long)st.st_uid);
		fprintf(stderr, "gid: %ld\n", (long)st.st_gid);
		fprintf(stderr, "mode: %ld\n", (unsigned long)st.st_mode & (S_IRWXU | S_IRWXG | S_IRWXO));
		if (S_ISDIR(st.st_mode)) {
			__do_closedir DIR *fdir = NULL;
			struct dirent *de;

			fdir = fdopendir(container_fd);
			if (!fdir) {
				error("error: fdopendir");
				return -1;
			}
			move_fd(container_fd);

			fprintf(stderr, "type: directory\n");

			while((de = readdir(fdir))) {
				int len, i;

				if (!strcmp(de->d_name, ".") || !strcmp(de->d_name, ".."))
					continue;

				fprintf(stderr, "entry: ");

				// swap \n to \0 since we split this output by line
				for (i = 0, len = strlen(de->d_name); i < len; i++) {
					if (*(de->d_name + i) == '\n')
						putc(0, stderr);
					else
						putc(*(de->d_name + i), stderr);
				}
				fprintf(stderr, "\n");
			}

			// container_fd is dead now that we fdopendir'd it
			return -1;
		} else {
			fprintf(stderr, "type: file\n");
			fret = copy(host_fd, container_fd, false);
		}
		fprintf(stderr, "type: %s", S_ISDIR(st.st_mode) ? "directory" : "file");
	}

	return fret;
}

static void forkdofile(bool is_put, char *rootfs, int pidfd, int ns_fd)
{
	struct file_args args = {
		.uid		= 0,
		.gid		= 0,
		.uid_default	= 0,
		.gid_default	= 0,
		.mode		= 0,
		.mode_default	= 0,
		.append		= false,
		.is_put		= is_put,
		.rootfs		= rootfs,
		.pidfd		= pidfd,
		.ns_fd		= ns_fd,
	};
	char *cur = NULL;

	cur = advance_arg(true);
	if (is_put)
		args.host = cur;
	else
		args.container = cur;

	cur = advance_arg(true);
	if (is_put)
		args.container = cur;
	else
		args.host = cur;

	if (is_put) {
		args.type		= advance_arg(true);
		args.uid		= atoi(advance_arg(true));
		args.gid		= atoi(advance_arg(true));
		args.mode		= atoi(advance_arg(true));
		args.uid_default	= atoi(advance_arg(true));
		args.gid_default	= atoi(advance_arg(true));
		args.mode_default	= atoi(advance_arg(true));

		if (strcmp(advance_arg(true), "append") == 0)
			args.append = true;
	}

	printf("%d: %s to %s\n", args.is_put, args.host, args.container);

	_exit(manip_file_in_ns(&args));
}

static void forkcheckfile(char *rootfs, int pidfd, int ns_fd)
{
	char *path = NULL;

	path = advance_arg(true);

	if (ns_fd >= 0) {
		attach_userns_fd(ns_fd);

		if (!change_namespaces(pidfd, ns_fd, CLONE_NEWNS)) {
			error("error: setns");
			_exit(1);
		}
	} else {
		if (chroot(rootfs) < 0) {
			error("error: chroot");
			_exit(1);
		}

		if (chdir("/") < 0) {
			error("error: chdir");
			_exit(1);
		}
	}

	if (access(path, F_OK) < 0) {
		fprintf(stderr, "Path doesn't exist: %s\n", strerror(errno));
		_exit(1);
	}

	_exit(0);
}

static void forkremovefile(char *rootfs, int pidfd, int ns_fd)
{
	char *path = NULL;
	struct stat sb;

	path = advance_arg(true);

	if (ns_fd >= 0) {
		attach_userns_fd(ns_fd);

		if (!change_namespaces(pidfd, ns_fd, CLONE_NEWNS)) {
			error("error: setns");
			_exit(1);
		}
	} else {
		if (chroot(rootfs) < 0) {
			error("error: chroot");
			_exit(1);
		}

		if (chdir("/") < 0) {
			error("error: chdir");
			_exit(1);
		}
	}

	if (stat(path, &sb) < 0) {
		error("error: stat");
		_exit(1);
	}

	if ((sb.st_mode & S_IFMT) == S_IFDIR) {
		if (rmdir(path) < 0) {
			fprintf(stderr, "Failed to remove %s: %s\n", path, strerror(errno));
			_exit(1);
		}
	} else {
		if (unlink(path) < 0) {
			fprintf(stderr, "Failed to remove %s: %s\n", path, strerror(errno));
			_exit(1);
		}
	}

	_exit(0);
}

void forkfile(void)
{
	int ns_fd = -EBADF, pidfd = -EBADF;
	char *command = NULL;
	char *rootfs = NULL;
	pid_t pid = 0;

	// Get the subcommand
	command = advance_arg(false);
	if (command == NULL || (strcmp(command, "--help") == 0 || strcmp(command, "--version") == 0 || strcmp(command, "-h") == 0)) {
		return;
	}

	// Get the container rootfs
	rootfs = advance_arg(false);
	if (rootfs == NULL || (strcmp(rootfs, "--help") == 0 || strcmp(rootfs, "--version") == 0 || strcmp(rootfs, "-h") == 0)) {
		return;
	}

	// Check that we're root
	if (geteuid() != 0) {
		fprintf(stderr, "Error: forkfile requires root privileges\n");
		_exit(1);
	}

	// Get the container PID
	pid = atoi(advance_arg(true));
	pidfd = atoi(advance_arg(true));

	if (pid > 0 || pidfd >= 0) {
		ns_fd = pidfd_nsfd(pidfd, pid);
		if (ns_fd < 0)
			_exit(1);
	}

	// Call the subcommands
	if (strcmp(command, "push") == 0) {
		forkdofile(true, rootfs, pidfd, ns_fd);
	} else if (strcmp(command, "pull") == 0) {
		forkdofile(false, rootfs, pidfd, ns_fd);
	} else if (strcmp(command, "exists") == 0) {
		forkcheckfile(rootfs, pidfd, ns_fd);
	} else if (strcmp(command, "remove") == 0) {
		forkremovefile(rootfs, pidfd, ns_fd);
	}
}
*/
import "C"

type cmdForkfile struct {
	global *cmdGlobal
}

func (c *cmdForkfile) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkfile"
	cmd.Short = "Perform container file operations"
	cmd.Long = `Description:
  Perform container file operations

  This set of internal commands are used for all container file
  operations, attaching to the container's root filesystem and mount
  namespace.
`
	cmd.Hidden = true

	// pull
	cmdPull := &cobra.Command{}
	cmdPull.Use = "pull <rootfs> <PID> <PidFd> <source> <destination>"
	cmdPull.Args = cobra.ExactArgs(5)
	cmdPull.RunE = c.Run
	cmd.AddCommand(cmdPull)

	// push
	cmdPush := &cobra.Command{}
	cmdPush.Use = "push <rootfs> <PID> <PidFd> <source> <destination> <type> <uid> <gid> <mode> <root uid> <root gid> <default mode> <write type>"
	cmdPush.Args = cobra.ExactArgs(13)
	cmdPush.RunE = c.Run
	cmd.AddCommand(cmdPush)

	// exists
	cmdExists := &cobra.Command{}
	cmdExists.Use = "exists <rootfs> <PID> <PidFd> <path>"
	cmdExists.Args = cobra.ExactArgs(4)
	cmdExists.RunE = c.Run
	cmd.AddCommand(cmdExists)

	// remove
	cmdRemove := &cobra.Command{}
	cmdRemove.Use = "remove <rootfs> <PID> <PidFd> <path>"
	cmdRemove.Args = cobra.ExactArgs(4)
	cmdRemove.RunE = c.Run
	cmd.AddCommand(cmdRemove)

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { cmd.Usage() }
	return cmd
}

func (c *cmdForkfile) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("This command should have been intercepted in cgo")
}
