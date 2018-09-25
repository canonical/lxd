package main

/*
#define _GNU_SOURCE
#include <errno.h>
#include <sched.h>
#include <stdio.h>
#include <string.h>
#include <sys/mount.h>
#include <sys/types.h>
#include <unistd.h>

__attribute__((constructor)) void init(void) {
	if (geteuid() != 0) {
		return;
	}

	// Unshare a new mntns so our mounts don't leak
	if (unshare(CLONE_NEWNS) < 0) {
		fprintf(stderr, "Failed to unshare a new mount namespace: %s\n", strerror(errno));
		_exit(1);
	}

	// Prevent mount propagation back to initial namespace
	if (mount(NULL, "/", NULL, MS_REC | MS_PRIVATE, NULL) < 0) {
		fprintf(stderr, "Failed to mark / private: %s\n", strerror(errno));
		_exit(1);
	}

	// We're done, jump back to Go
}
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
import "C"
