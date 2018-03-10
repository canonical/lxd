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
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <grp.h>
#include <libgen.h>
#include <linux/limits.h>
#include <sched.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#define CMDLINE_SIZE (8 * PATH_MAX)

#define ADVANCE_ARG_REQUIRED() \
	do { \
		while (*cur != 0) \
			cur++; \
		cur++; \
		if (size <= cur - buf) { \
			fprintf(stderr, "not enough arguments\n"); \
			_exit(1); \
		} \
	} while(0)

extern void forkfile(char *buf, char *cur, ssize_t size);

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

void forkgetnet(char *buf, char *cur, ssize_t size) {
	ADVANCE_ARG_REQUIRED();
	int pid = atoi(cur);

	if (dosetns(pid, "net") < 0) {
		fprintf(stderr, "Failed setns to container network namespace: %s\n", strerror(errno));
		_exit(1);
	}

	// The rest happens in Go
}

void forkproxy(char *buf, char *cur, ssize_t size) {
	int cmdline, listen_pid, connect_pid, fdnum, forked, childPid, ret;
	char fdpath[80];
	char *logPath = NULL, *pidPath = NULL;
	FILE *logFile = NULL, *pidFile = NULL;

	// Get the arguments
	ADVANCE_ARG_REQUIRED();
	listen_pid = atoi(cur);
	ADVANCE_ARG_REQUIRED();
	ADVANCE_ARG_REQUIRED();
	connect_pid = atoi(cur);
	ADVANCE_ARG_REQUIRED();
	ADVANCE_ARG_REQUIRED();
	fdnum = atoi(cur);
	ADVANCE_ARG_REQUIRED();
	forked = atoi(cur);
	ADVANCE_ARG_REQUIRED();
	logPath = cur;
	ADVANCE_ARG_REQUIRED();
	pidPath = cur;

	// Check if proxy daemon already forked
	if (forked == 0) {
		logFile = fopen(logPath, "w+");
		if (logFile == NULL) {
			_exit(1);
		}

		if (dup2(fileno(logFile), STDOUT_FILENO) < 0) {
			fprintf(logFile, "Failed to redirect STDOUT to logfile: %s\n", strerror(errno));
			_exit(1);
		}
		if (dup2(fileno(logFile), STDERR_FILENO) < 0) {
			fprintf(logFile, "Failed to redirect STDERR to logfile: %s\n", strerror(errno));
			_exit(1);
		}
		fclose(logFile);

		pidFile = fopen(pidPath, "w+");
		if (pidFile == NULL) {
			fprintf(stderr, "Failed to create pid file for proxy daemon: %s\n", strerror(errno));
			_exit(1);
		}

		childPid = fork();
		if (childPid < 0) {
			fprintf(stderr, "Failed to fork proxy daemon: %s\n", strerror(errno));
			_exit(1);
		} else if (childPid != 0) {
			fprintf(pidFile, "%d", childPid);
			fclose(pidFile);
			fclose(stdin);
			fclose(stdout);
			fclose(stderr);
			_exit(0);
		} else {
			ret = setsid();
			if (ret < 0) {
				fprintf(stderr, "Failed to setsid in proxy daemon: %s\n", strerror(errno));
				_exit(1);
			}
		}
	}

	// Cannot pass through -1 to runCommand since it is interpreted as a flag
	fdnum = fdnum == 0 ? -1 : fdnum;

	ret = snprintf(fdpath, sizeof(fdpath), "/proc/self/fd/%d", fdnum);
	if (ret < 0 || (size_t)ret >= sizeof(fdpath)) {
		fprintf(stderr, "Failed to format file descriptor path\n");
		_exit(1);
	}

	// Join the listener ns if not already setup
	if (access(fdpath, F_OK) < 0) {
		// Attach to the network namespace of the listener
		if (dosetns(listen_pid, "net") < 0) {
			fprintf(stderr, "Failed setns to listener network namespace: %s\n", strerror(errno));
			_exit(1);
		}
	} else {
		// Join the connector ns now
		if (dosetns(connect_pid, "net") < 0) {
			fprintf(stderr, "Failed setns to connector network namespace: %s\n", strerror(errno));
			_exit(1);
		}
	}
}

__attribute__((constructor)) void init(void) {
	int cmdline;
	char buf[CMDLINE_SIZE];
	ssize_t size;
	char *cur;

	// Extract arguments
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

	// Skip the first argument (but don't fail on missing second argument)
	cur = buf;
	while (*cur != 0)
		cur++;
	cur++;
	if (size <= cur - buf)
		return;

	// Intercepts some subcommands
	if (strcmp(cur, "forkfile") == 0) {
		forkfile(buf, cur, size);
	} else if (strcmp(cur, "forkmount") == 0) {
		forkmount(buf, cur, size);
	} else if (strcmp(cur, "forkumount") == 0) {
		forkumount(buf, cur, size);
	} else if (strcmp(cur, "forkgetnet") == 0) {
		forkgetnet(buf, cur, size);
	} else if (strcmp(cur, "forkproxy") == 0) {
		forkproxy(buf, cur, size);
	}
}
*/
import "C"
