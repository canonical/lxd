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
#include <unistd.h>

// External functions
extern void forkfile();
extern void forkmount();
extern void forknet();
extern void forkproxy();

// Command line parsing and tracking
#define CMDLINE_SIZE (8 * PATH_MAX)
char cmdline_buf[CMDLINE_SIZE];
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
	char userns_source[22];
	char userns_target[22];
	ssize_t len = 0;

	sprintf(nspath, "/proc/%d/ns/user", pid);
	if (access(nspath, F_OK) == 0) {
		len = readlink("/proc/self/ns/user", userns_source, 21);
		if (len < 0) {
			fprintf(stderr, "Failed readlink of source namespace: %s\n", strerror(errno));
			_exit(1);
		}
		userns_source[len] = '\0';

		len = readlink(nspath, userns_target, 21);
		if (len < 0) {
			fprintf(stderr, "Failed readlink of target namespace: %s\n", strerror(errno));
			_exit(1);
		}
		userns_target[len] = '\0';

		if (strcmp(userns_source, userns_target) != 0) {
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

__attribute__((constructor)) void init(void) {
	int cmdline;

	// Extract arguments
	cmdline = open("/proc/self/cmdline", O_RDONLY);
	if (cmdline < 0) {
		error("error: open");
		_exit(232);
	}

	memset(cmdline_buf, 0, sizeof(cmdline_buf));
	if ((cmdline_size = read(cmdline, cmdline_buf, sizeof(cmdline_buf)-1)) < 0) {
		close(cmdline);
		error("error: read");
		_exit(232);
	}
	close(cmdline);

	// Skip the first argument (but don't fail on missing second argument)
	cmdline_cur = cmdline_buf;
	while (*cmdline_cur != 0)
		cmdline_cur++;
	cmdline_cur++;
	if (cmdline_size <= cmdline_cur - cmdline_buf)
		return;

	// Intercepts some subcommands
	if (strcmp(cmdline_cur, "forkfile") == 0) {
		forkfile();
	} else if (strcmp(cmdline_cur, "forkmount") == 0) {
		forkmount();
	} else if (strcmp(cmdline_cur, "forknet") == 0) {
		forknet();
	} else if (strcmp(cmdline_cur, "forkproxy") == 0) {
		forkproxy();
	}
}
*/
import "C"
