package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

/*
#define _GNU_SOURCE
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>
#include <limits.h>

extern char* advance_arg(bool required);
extern void error(char *msg);
extern void attach_userns(int pid);
extern int dosetns(int pid, char *nstype);

int copy(int target, int source, bool append)
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

int manip_file_in_ns(char *rootfs, int pid, char *host, char *container, bool is_put, char *type, uid_t uid, gid_t gid, mode_t mode, uid_t defaultUid, gid_t defaultGid, mode_t defaultMode, bool append) {
	int host_fd = -1, container_fd = -1;
	int ret = -1;
	int container_open_flags;
	struct stat st;
	int exists = 1;
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

	if (pid > 0) {
		attach_userns(pid);

		if (dosetns(pid, "mnt") < 0) {
			error("error: setns");
			goto close_host;
		}
	} else {
		if (chroot(rootfs) < 0) {
			error("error: chroot");
			goto close_host;
		}

		if (chdir("/") < 0) {
			error("error: chdir");
			goto close_host;
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
		goto close_host;
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
			goto close_host;
		}
		link_target[link_length] = '\0';

		dprintf(host_fd, "%s\n", link_target);
		goto close_container;
	}

	umask(0);
	container_fd = open(container, container_open_flags, 0);
	if (container_fd < 0) {
		error("error: open");
		goto close_host;
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
			goto close_container;
		}

		if (mode != -1 && fchmod(container_fd, mode) < 0) {
			error("error: chmod");
			goto close_container;
		}

		if (fchown(container_fd, uid, gid) < 0) {
			error("error: chown");
			goto close_container;
		}
		ret = 0;
	} else {
		if (fstat(container_fd, &st) < 0) {
			error("error: stat");
			goto close_container;
		}

		fprintf(stderr, "uid: %ld\n", (long)st.st_uid);
		fprintf(stderr, "gid: %ld\n", (long)st.st_gid);
		fprintf(stderr, "mode: %ld\n", (unsigned long)st.st_mode & (S_IRWXU | S_IRWXG | S_IRWXO));
		if (S_ISDIR(st.st_mode)) {
			DIR *fdir;
			struct dirent *de;

			fdir = fdopendir(container_fd);
			if (!fdir) {
				error("error: fdopendir");
				goto close_container;
			}

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

			closedir(fdir);
			// container_fd is dead now that we fdopendir'd it
			goto close_host;
		} else {
			fprintf(stderr, "type: file\n");
			ret = copy(host_fd, container_fd, false);
		}
		fprintf(stderr, "type: %s", S_ISDIR(st.st_mode) ? "directory" : "file");
	}

close_container:
	close(container_fd);
close_host:
	close(host_fd);
	return ret;
}

void forkdofile(bool is_put, char *rootfs, pid_t pid) {
	char *cur = NULL;

	uid_t uid = 0;
	uid_t defaultUid = 0;

	gid_t gid = 0;
	gid_t defaultGid = 0;

	mode_t mode = 0;
	mode_t defaultMode = 0;

	char *source = NULL;
	char *target = NULL;
	char *writeMode = NULL;
	char *type = NULL;

	bool append = false;


	cur = advance_arg(true);
	if (is_put) {
		source = cur;
	} else {
		target = cur;
	}

	cur = advance_arg(true);
	if (is_put) {
		target = cur;
	} else {
		source = cur;
	}

	if (is_put) {
		type = advance_arg(true);
		uid = atoi(advance_arg(true));
		gid = atoi(advance_arg(true));
		mode = atoi(advance_arg(true));
		defaultUid = atoi(advance_arg(true));
		defaultGid = atoi(advance_arg(true));
		defaultMode = atoi(advance_arg(true));

		if (strcmp(advance_arg(true), "append") == 0) {
			append = true;
		}
	}

	printf("%d: %s to %s\n", is_put, source, target);

	_exit(manip_file_in_ns(rootfs, pid, source, target, is_put, type, uid, gid, mode, defaultUid, defaultGid, defaultMode, append));
}

void forkcheckfile(char *rootfs, pid_t pid) {
	char *path = NULL;

	path = advance_arg(true);

	if (pid > 0) {
		attach_userns(pid);

		if (dosetns(pid, "mnt") < 0) {
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

void forkremovefile(char *rootfs, pid_t pid) {
	char *path = NULL;
	struct stat sb;

	path = advance_arg(true);

	if (pid > 0) {
		attach_userns(pid);

		if (dosetns(pid, "mnt") < 0) {
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

void forkfile() {
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

	// Get the container PID
	pid = atoi(advance_arg(true));

	// Check that we're root
	if (geteuid() != 0) {
		fprintf(stderr, "Error: forkfile requires root privileges\n");
		_exit(1);
	}

	// Call the subcommands
	if (strcmp(command, "push") == 0) {
		forkdofile(true, rootfs, pid);
	} else if (strcmp(command, "pull") == 0) {
		forkdofile(false, rootfs, pid);
	} else if (strcmp(command, "exists") == 0) {
		forkcheckfile(rootfs, pid);
	} else if (strcmp(command, "remove") == 0) {
		forkremovefile(rootfs, pid);
	}
}
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
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
	cmdPull.Use = "pull <rootfs> <PID> <source> <destination>"
	cmdPull.Args = cobra.ExactArgs(4)
	cmdPull.RunE = c.Run
	cmd.AddCommand(cmdPull)

	// push
	cmdPush := &cobra.Command{}
	cmdPush.Use = "push <rootfs> <PID> <source> <destination> <type> <uid> <gid> <mode> <root uid> <root gid> <default mode> <write type>"
	cmdPush.Args = cobra.ExactArgs(12)
	cmdPush.RunE = c.Run
	cmd.AddCommand(cmdPush)

	// exists
	cmdExists := &cobra.Command{}
	cmdExists.Use = "exists <rootfs> <PID> <path>"
	cmdExists.Args = cobra.ExactArgs(3)
	cmdExists.RunE = c.Run
	cmd.AddCommand(cmdExists)

	// remove
	cmdRemove := &cobra.Command{}
	cmdRemove.Use = "remove <rootfs> <PID> <path>"
	cmdRemove.Args = cobra.ExactArgs(3)
	cmdRemove.RunE = c.Run
	cmd.AddCommand(cmdRemove)

	return cmd
}

func (c *cmdForkfile) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("This command should have been intercepted in cgo")
}
