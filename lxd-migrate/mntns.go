package main

/*
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <linux/limits.h>
#include <sched.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

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

int attach_mntns() {
	if (dosetns(1, "mnt") < 0) {
		error("error: setns");
		return -1;
	}

	if (chdir("/") < 0) {
		error("error: chdir");
		return -1;
	}

	return 0;
}

__attribute__((constructor)) void init(void) {
	int ret = 0;

	if (geteuid() == 0) {
		ret = attach_mntns();
	}

	if (ret < 0) {
		_exit(ret);
	}
}
*/
import "C"
