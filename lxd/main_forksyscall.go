package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

/*
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <fcntl.h>
#include <libgen.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/capability.h>
#include <sys/fsuid.h>
#include <sys/prctl.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/vfs.h>
#include <unistd.h>

#include "include/memory_utils.h"

extern char* advance_arg(bool required);
extern int dosetns(int pid, char *nstype);

static uid_t get_ns_uid(uid_t uid, pid_t pid)
{
        __do_free char *line = NULL;
        __do_fclose FILE *f = NULL;
        size_t sz = 0;
	char path[256];
        uid_t nsid, hostid, range;

	snprintf(path, sizeof(path), "/proc/%d/uid_map", pid);
	f = fopen(path, "re");
	if (!f)
		return -1;

        while (getline(&line, &sz, f) != -1) {
                if (sscanf(line, "%u %u %u", &nsid, &hostid, &range) != 3)
                        continue;

                if (nsid <= uid && nsid + range > uid) {
                        nsid += uid - hostid;
			return nsid;
                }
        }

        return -1;
}

static gid_t get_ns_gid(uid_t gid, pid_t pid)
{
        __do_free char *line = NULL;
        __do_fclose FILE *f = NULL;
        size_t sz = 0;
	char path[256];
        uid_t nsid, hostid, range;

	snprintf(path, sizeof(path), "/proc/%d/gid_map", pid);
	f = fopen(path, "re");
	if (!f)
		return -1;

        while (getline(&line, &sz, f) != -1) {
                if (sscanf(line, "%u %u %u", &nsid, &hostid, &range) != 3)
                        continue;

                if (nsid <= gid && nsid + range > gid) {
                        nsid += gid - hostid;
			return nsid;
                }
        }

        return -1;
}

static inline bool same_fsinfo(struct stat *s1, struct stat *s2,
			       struct statfs *sfs1, struct statfs *sfs2)
{
	return ((sfs1->f_type == sfs2->f_type) && (s1->st_dev == s2->st_dev) && (s1->st_ino == s2->st_ino));
}

static int fstat_fstatfs(int fd, struct stat *s, struct statfs *sfs)
{
	if (fstat(fd, s))
		return -1;

	if (fstatfs(fd, sfs))
		return -1;

	return 0;
}

// Expects command line to be in the form:
// <PID> <root-uid> <root-gid> <path> <mode> <dev>
static void forkmknod()
{
	__do_close_prot_errno int target_fd = -EBADF, host_target_fd = -EBADF;
	int ret;
	char *cur = NULL, *target = NULL, *target_host = NULL;
	char cwd[256];
	mode_t mode = 0;
	dev_t dev = 0;
	pid_t pid = 0;
	uid_t uid = -1;
	gid_t gid = -1;
	struct stat s1, s2;
	struct statfs sfs1, sfs2;
	cap_t caps;
	int chk_perm_only;

	pid = atoi(advance_arg(true));
	target = advance_arg(true);
	mode = atoi(advance_arg(true));
	dev = atoi(advance_arg(true));
	target_host = advance_arg(true);
	uid = atoi(advance_arg(true));
	gid = atoi(advance_arg(true));
	chk_perm_only = atoi(advance_arg(true));

	snprintf(cwd, sizeof(cwd), "/proc/%d/cwd", pid);
	target_fd = open(cwd, O_PATH | O_RDONLY | O_CLOEXEC);
	if (target_fd < 0) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	host_target_fd = open(dirname(target_host), O_PATH | O_RDONLY | O_CLOEXEC);
	if (host_target_fd < 0) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	(void)dosetns(pid, "mnt");

	caps = cap_get_pid(pid);
	if (!caps) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	ret = prctl(PR_SET_KEEPCAPS, 1);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	ret = setegid(gid);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	setfsgid(gid);

	ret = seteuid(uid);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	setfsuid(uid);

	ret = cap_set_proc(caps);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	ret = fstat_fstatfs(target_fd, &s2, &sfs2);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	if (sfs2.f_flags & MS_NODEV) {
		fprintf(stderr, "%d", EPERM);
		_exit(EXIT_FAILURE);
	}

	ret = fstat_fstatfs(host_target_fd, &s1, &sfs1);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	if (!same_fsinfo(&s1, &s2, &sfs1, &sfs2)) {
		fprintf(stderr, "%d", ENOMEDIUM);
		_exit(EXIT_FAILURE);
	}

	if (chk_perm_only) {
		fprintf(stderr, "%d", ENOMEDIUM);
		_exit(EXIT_FAILURE);
	}

	// basename() can modify its argument so accessing target_host is
	// invalid from now on.
	ret = mknodat(target_fd, target, mode, dev);
	if (ret) {
		fprintf(stderr, "%d", errno);
		_exit(EXIT_FAILURE);
	}

}

void forksyscall()
{
	char *syscall = NULL;

	// Check that we're root
	if (geteuid() != 0)
		_exit(EXIT_FAILURE);

	// Get the subcommand
	syscall = advance_arg(false);
	if (syscall == NULL ||
	    (strcmp(syscall, "--help") == 0 ||
	     strcmp(syscall, "--version") == 0 || strcmp(syscall, "-h") == 0))
		_exit(EXIT_SUCCESS);

	if (strcmp(syscall, "mknod") == 0)
		forkmknod();
	else
		_exit(EXIT_FAILURE);

	_exit(EXIT_SUCCESS);
}
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
// #cgo LDFLAGS: -lcap
import "C"

type cmdForksyscall struct {
	global *cmdGlobal
}

func GetNSUid(uid uint, pid int) int {
	return int(C.get_ns_uid(C.uid_t(uid), C.pid_t(pid)))
}

func GetNSGid(gid uint, pid int) int {
	return int(C.get_ns_gid(C.gid_t(gid), C.pid_t(pid)))
}

func (c *cmdForksyscall) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forksyscall <syscall> <PID> <path> <mode> <dev>"
	cmd.Short = "Perform syscall operations"
	cmd.Long = `Description:
  Perform syscall operations

  This set of internal commands are used for all seccom-based container syscall
  operations.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForksyscall) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("This command should have been intercepted in cgo")
}
