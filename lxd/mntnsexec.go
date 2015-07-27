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
#include <string.h>
#include <stdio.h>
#include <sys/mount.h>
#include <sched.h>
#include <linux/sched.h>
#include <linux/limits.h>
#include <sys/mman.h>
#include <fcntl.h>
#include <stdbool.h>
#include <unistd.h>

// This expects:
//  ./lxd forkputfile /source/path <pid> /target/path
// or
//  ./lxd forkgetfile /target/path <pid> /soruce/path <uid> <gid> <mode>
// i.e. 8 arguments, each which have a max length of PATH_MAX.
// Unfortunately, lseek() and fstat() both fail (EINVAL and 0 size) for
// procfs. Also, we can't mmap, because procfs doesn't support that, either.
//
#define CMDLINE_SIZE (8 * PATH_MAX)

int copy(int target, int source)
{
	ssize_t n;
	char buf[1024];

	while ((n = read(source, buf, 1024)) > 0) {
		if (write(target, buf, n) != n) {
			perror("write");
			return -1;
		}
	}

	if (n < 0) {
		perror("read");
		return -1;
	}

	return 0;
}

int dosetns(int pid, char *nstype) {
	int mntns;
	char buf[PATH_MAX];

	sprintf(buf, "/proc/%d/ns/%s", pid, nstype);
	fprintf(stderr, "mntns dir: %s\n", buf);
	mntns = open(buf, O_RDONLY);
	if (mntns < 0) {
		perror("open mntns");
		return -1;
	}

	if (setns(mntns, 0) < 0) {
		perror("setns");
		close(mntns);
		return -1;
	}
	close(mntns);

	return 0;
}

int manip_file_in_ns(char *host, int pid, char *container, bool is_put, uid_t uid, gid_t gid, mode_t mode) {
	int host_fd, container_fd;
	int ret = -1;
	int container_open_flags;

	host_fd = open(host, O_RDWR);
	if (host_fd < 0) {
		perror("open host");
		return -1;
	}

	container_open_flags = O_RDWR;
	if (is_put)
		container_open_flags |= O_CREAT;

	if (dosetns(pid, "mnt") < 0)
		goto close_host;

	container_fd = open(container, container_open_flags, mode);
	if (container_fd < 0) {
		perror("open container");
		goto close_host;
	}

	if (is_put) {
		if (copy(container_fd, host_fd) < 0)
			goto close_container;

		if (fchown(container_fd, uid, gid) < 0) {
			perror("fchown");
			goto close_container;
		}

		ret = 0;
	} else
		ret = copy(host_fd, container_fd);

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
			printf("not enough arguments\n");	\
			_exit(1);				\
		}						\
	} while(0)

void forkdofile(char *buf, char *cur, bool is_put, ssize_t size) {
	uid_t uid = 0;
	gid_t gid = 0;
	mode_t mode = 0;
	char *command = cur, *source = NULL, *target = NULL;
	pid_t pid;

	ADVANCE_ARG_REQUIRED();
	source = cur;

	ADVANCE_ARG_REQUIRED();
	pid = atoi(cur);

	ADVANCE_ARG_REQUIRED();
	target = cur;

	if (is_put) {
		ADVANCE_ARG_REQUIRED();
		uid = atoi(cur);

		ADVANCE_ARG_REQUIRED();
		gid = atoi(cur);

		ADVANCE_ARG_REQUIRED();
		mode = atoi(cur);
	}

	printf("command: %s\n", command);
	printf("source: %s\n", source);
	printf("pid: %d\n", pid);
	printf("target: %s\n", target);
	printf("uid: %d\n", uid);
	printf("gid: %d\n", gid);
	printf("mode: %d\n", mode);

	_exit(manip_file_in_ns(source, pid, target, is_put, uid, gid, mode));
}

__attribute__((constructor)) void init(void) {
	int cmdline;
	char buf[CMDLINE_SIZE];
	ssize_t size;
	char *cur;

	cmdline = open("/proc/self/cmdline", O_RDONLY);
	if (cmdline < 0) {
		perror("open");
		_exit(232);
	}

	memset(buf, 0, sizeof(buf));
	if ((size = read(cmdline, buf, sizeof(buf)-1)) < 0) {
		close(cmdline);
		perror("read");
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
	} else if (strcmp(cur, "forkmount") == 0) {
		return;
	} else if (strcmp(cur, "forkumount") == 0) {
		return;
	}
}
*/
import "C"
