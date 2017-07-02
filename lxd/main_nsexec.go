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
#include <string.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/mount.h>
#include <sched.h>
#include <linux/sched.h>
#include <linux/limits.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <fcntl.h>
#include <stdbool.h>
#include <unistd.h>
#include <errno.h>
#include <alloca.h>
#include <libgen.h>
#include <ifaddrs.h>
#include <dirent.h>
#include <grp.h>

// This expects:
//  ./lxd forkputfile /source/path <pid> /target/path
// or
//  ./lxd forkgetfile /target/path <pid> /soruce/path <uid> <gid> <mode>
// i.e. 8 arguments, each which have a max length of PATH_MAX.
// Unfortunately, lseek() and fstat() both fail (EINVAL and 0 size) for
// procfs. Also, we can't mmap, because procfs doesn't support that, either.
//
#define CMDLINE_SIZE (8 * PATH_MAX)

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

int dosetns(int pid, char *nstype) {
	int mntns;
	char buf[PATH_MAX];

	sprintf(buf, "/proc/%d/ns/%s", pid, nstype);
	mntns = open(buf, O_RDONLY);
	if (mntns < 0) {
		error("error: open mntns");
		return -1;
	}

	if (setns(mntns, 0) < 0) {
		error("error: setns");
		close(mntns);
		return -1;
	}
	close(mntns);

	return 0;
}

void attach_userns(int pid) {
	char nspath[PATH_MAX];
	char userns_source[PATH_MAX];
	char userns_target[PATH_MAX];

	sprintf(nspath, "/proc/%d/ns/user", pid);
	if (access(nspath, F_OK) == 0) {
		if (readlink("/proc/self/ns/user", userns_source, 18) < 0) {
			fprintf(stderr, "Failed readlink of source namespace: %s\n", strerror(errno));
			_exit(1);
		}

		if (readlink(nspath, userns_target, PATH_MAX) < 0) {
			fprintf(stderr, "Failed readlink of target namespace: %s\n", strerror(errno));
			_exit(1);
		}

		if (strncmp(userns_source, userns_target, PATH_MAX) != 0) {
			if (dosetns(pid, "user") < 0) {
				fprintf(stderr, "Failed setns to container user namespace: %s\n", strerror(errno));
				_exit(1);
			}

			if (setgroups(0, NULL) < 0) {
				fprintf(stderr, "Failed setgroups to container root groups: %s\n", strerror(errno));
				_exit(1);
			}

			if (setgid(0) < 0) {
				fprintf(stderr, "Failed setgid to container root group: %s\n", strerror(errno));
				_exit(1);
			}

			if (setuid(0) < 0) {
				fprintf(stderr, "Failed setuid to container root user: %s\n", strerror(errno));
				_exit(1);
			}

		}
	}
}

int manip_file_in_ns(char *rootfs, int pid, char *host, char *container, bool is_put, char *type, uid_t uid, gid_t gid, mode_t mode, uid_t defaultUid, gid_t defaultGid, mode_t defaultMode, bool append) {
	int host_fd = -1, container_fd = -1;
	int ret = -1;
	int container_open_flags;
	struct stat st;
	int exists = 1;
	bool is_dir_manip = type != NULL && !strcmp(type, "directory");
	bool is_symlink_manip = type != NULL && !strcmp(type, "symlink");

	if (!is_dir_manip && !is_symlink_manip) {
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

	if (stat(container, &st) < 0)
		exists = 0;

	container_open_flags = O_RDWR;
	if (is_put)
		container_open_flags |= O_CREAT;

	if (is_put && !is_dir_manip && exists && S_ISDIR(st.st_mode)) {
		error("error: Path already exists as a directory");
		goto close_host;
	}

	if (exists && S_ISDIR(st.st_mode))
		container_open_flags = O_DIRECTORY;

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

#define ADVANCE_ARG_REQUIRED()					\
	do {							\
		while (*cur != 0)				\
			cur++;					\
		cur++;						\
		if (size <= cur - buf) {			\
			fprintf(stderr, "not enough arguments\n");	\
			_exit(1);				\
		}						\
	} while(0)

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

void forkmount(char *buf, char *cur, ssize_t size) {
	char *src, *dest, *opts;

	ADVANCE_ARG_REQUIRED();
	int pid = atoi(cur);

	attach_userns(pid);

	if (dosetns(pid, "mnt") < 0) {
		fprintf(stderr, "Failed setns to container mount namespace: %s\n", strerror(errno));
		_exit(1);
	}

	ADVANCE_ARG_REQUIRED();
	src = cur;

	ADVANCE_ARG_REQUIRED();
	dest = cur;

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

void forkumount(char *buf, char *cur, ssize_t size) {
	ADVANCE_ARG_REQUIRED();
	int pid = atoi(cur);

	if (dosetns(pid, "mnt") < 0) {
		fprintf(stderr, "Failed setns to container mount namespace: %s\n", strerror(errno));
		_exit(1);
	}

	ADVANCE_ARG_REQUIRED();
	if (access(cur, F_OK) < 0) {
		fprintf(stderr, "Mount path doesn't exist: %s\n", strerror(errno));
		_exit(1);
	}

	if (umount2(cur, MNT_DETACH) < 0) {
		fprintf(stderr, "Error unmounting %s: %s\n", cur, strerror(errno));
		_exit(1);
	}
	_exit(0);
}

void forkdofile(char *buf, char *cur, bool is_put, ssize_t size) {
	uid_t uid = 0;
	gid_t gid = 0;
	mode_t mode = 0;
	uid_t defaultUid = 0;
	gid_t defaultGid = 0;
	mode_t defaultMode = 0;
	char *command = cur, *rootfs = NULL, *source = NULL, *target = NULL, *writeMode = NULL, *type = NULL;
	pid_t pid;
	bool append = false;

	ADVANCE_ARG_REQUIRED();
	rootfs = cur;

	ADVANCE_ARG_REQUIRED();
	pid = atoi(cur);

	ADVANCE_ARG_REQUIRED();
	source = cur;

	ADVANCE_ARG_REQUIRED();
	target = cur;

	if (is_put) {
		ADVANCE_ARG_REQUIRED();
		type = cur;

		ADVANCE_ARG_REQUIRED();
		uid = atoi(cur);

		ADVANCE_ARG_REQUIRED();
		gid = atoi(cur);

		ADVANCE_ARG_REQUIRED();
		mode = atoi(cur);

		ADVANCE_ARG_REQUIRED();
		defaultUid = atoi(cur);

		ADVANCE_ARG_REQUIRED();
		defaultGid = atoi(cur);

		ADVANCE_ARG_REQUIRED();
		defaultMode = atoi(cur);

		ADVANCE_ARG_REQUIRED();
		if (strcmp(cur, "append") == 0) {
			append = true;
		}
	}

	_exit(manip_file_in_ns(rootfs, pid, source, target, is_put, type, uid, gid, mode, defaultUid, defaultGid, defaultMode, append));
}

void forkcheckfile(char *buf, char *cur, bool is_put, ssize_t size) {
	char *command = cur, *rootfs = NULL, *path = NULL;
	pid_t pid;

	ADVANCE_ARG_REQUIRED();
	rootfs = cur;

	ADVANCE_ARG_REQUIRED();
	pid = atoi(cur);

	ADVANCE_ARG_REQUIRED();
	path = cur;

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

void forkremovefile(char *buf, char *cur, bool is_put, ssize_t size) {
	char *command = cur, *rootfs = NULL, *path = NULL;
	pid_t pid;
	struct stat sb;

	ADVANCE_ARG_REQUIRED();
	rootfs = cur;

	ADVANCE_ARG_REQUIRED();
	pid = atoi(cur);

	ADVANCE_ARG_REQUIRED();
	path = cur;

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

void forkgetnet(char *buf, char *cur, ssize_t size) {
	ADVANCE_ARG_REQUIRED();
	int pid = atoi(cur);

	if (dosetns(pid, "net") < 0) {
		fprintf(stderr, "Failed setns to container network namespace: %s\n", strerror(errno));
		_exit(1);
	}

	// The rest happens in Go
}

__attribute__((constructor)) void init(void) {
	int cmdline;
	char buf[CMDLINE_SIZE];
	ssize_t size;
	char *cur;

	cmdline = open("/proc/self/cmdline", O_RDONLY);
	if (cmdline < 0) {
		error("error: open");
		_exit(232);
	}

	memset(buf, 0, sizeof(buf));
	if ((size = read(cmdline, buf, sizeof(buf)-1)) < 0) {
		close(cmdline);
		error("error: read");
		_exit(232);
	}
	close(cmdline);

	cur = buf;
	// skip argv[0]
	while (*cur != 0)
		cur++;
	cur++;
	if (size <= cur - buf)
		return;

	if (strcmp(cur, "forkputfile") == 0) {
		forkdofile(buf, cur, true, size);
	} else if (strcmp(cur, "forkgetfile") == 0) {
		forkdofile(buf, cur, false, size);
	} else if (strcmp(cur, "forkcheckfile") == 0) {
		forkcheckfile(buf, cur, false, size);
	} else if (strcmp(cur, "forkremovefile") == 0) {
		forkremovefile(buf, cur, false, size);
	} else if (strcmp(cur, "forkmount") == 0) {
		forkmount(buf, cur, size);
	} else if (strcmp(cur, "forkumount") == 0) {
		forkumount(buf, cur, size);
	} else if (strcmp(cur, "forkgetnet") == 0) {
		forkgetnet(buf, cur, size);
	}
}
*/
import "C"
