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
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/vfs.h>
#include <unistd.h>

#include "include/memory_utils.h"

extern char* advance_arg(bool required);
extern int dosetns(int pid, char *nstype);

static uid_t get_root_uid(pid_t pid)
{
	char *line = NULL;
	size_t sz = 0;
	uid_t nsid, hostid, range;
	FILE *f;
	char path[256];

	snprintf(path, sizeof(path), "/proc/%d/uid_map", pid);
	f = fopen(path, "re");
	if (!f)
		return -1;

	while (getline(&line, &sz, f) != -1) {
		if (sscanf(line, "%u %u %u", &nsid, &hostid, &range) != 3)
			continue;

		if (nsid == 0)
			return hostid;
	}

	nsid = -1;

found:
	fclose(f);
	free(line);
	return nsid;
}

static gid_t get_root_gid(pid_t pid)
{
	char *line = NULL;
	size_t sz = 0;
	gid_t nsid, hostid, range;
	FILE *f;
	char path[256];

	snprintf(path, sizeof(path), "/proc/%d/gid_map", pid);
	f = fopen(path, "re");
	if (!f)
		return -1;

	while (getline(&line, &sz, f) != -1) {
		if (sscanf(line, "%u %u %u", &nsid, &hostid, &range) != 3)
			continue;

		if (nsid == 0)
			return hostid;
	}

	nsid = -1;

found:
	fclose(f);
	free(line);
	return nsid;
}

static int chowmknod(const char *path, mode_t mode, dev_t dev,
			  uid_t uid, gid_t gid)
{
	int ret;

	ret = mknodat(AT_FDCWD, path, mode, dev);
	if (ret)
		return -1;

	return chown(path, uid, gid);
}

static int chdirchroot(pid_t pid)
{
	char path[256];

	snprintf(path, sizeof(path), "/proc/%d/cwd", pid);
	if (chdir(path))
		return -1;

	snprintf(path, sizeof(path), "/proc/%d/root", pid);
	return chroot(path);
}

static inline bool same_fsinfo(struct stat *s1, struct stat *s2,
			       struct statfs *sfs1, struct statfs *sfs2)
{
	return ((sfs1->f_type == sfs2->f_type) && (s1->st_dev == s2->st_dev) && (s1->st_ino == s2->st_ino));
}

static int stat_statfs(const char *path, struct stat *s, struct statfs *sfs)
{
	if (stat(path, s))
		return -1;

	if (statfs(path, sfs))
		return -1;

	return 0;
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
void forkmknod()
{
	__do_close_prot_errno int target_fd = -EBADF;
	__do_free char *target_host_dup = NULL;
	int ret;
	char *cur = NULL, *target = NULL, *target_host = NULL;
	mode_t mode = 0;
	dev_t dev = 0;
	pid_t pid = 0;
	uid_t uid = -1;
	gid_t gid = -1;
	struct stat s1, s2;
	struct statfs sfs1, sfs2;

	// Get the subcommand
	cur = advance_arg(false);
	if (!cur ||
	    (strcmp(cur, "--help") == 0 ||
	     strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0))
		return;

	// Check that we're root
	if (geteuid() != 0) {
		fprintf(stderr, "Error: forkmknod requires root privileges\n");
		_exit(EXIT_FAILURE);
	}

	pid = atoi(cur);
	target = advance_arg(true);
	mode = atoi(advance_arg(true));
	dev = atoi(advance_arg(true));
	target_host = advance_arg(true);

	uid = get_root_uid(pid);
	if (uid < 0)
		fprintf(stderr, "No root uid found (%d)\n", uid);

	gid = get_root_gid(pid);
	if (gid < 0)
		fprintf(stderr, "No root gid found (%d)\n", gid);

	// dirname() can modify its argument
	target_host_dup = strdup(target_host);
	if (!target_host_dup)
		_exit(EXIT_FAILURE);

	target_fd = open(dirname(target_host_dup), O_PATH | O_RDONLY | O_CLOEXEC);

	ret = chdirchroot(pid);
	if (ret)
		goto eperm;

	ret = chowmknod(target, mode, dev, uid, gid);
	if (ret) {
		if (errno == EEXIST) {
			fprintf(stderr, "%d", errno);
			_exit(EXIT_FAILURE);
		}
	} else {
		_exit(EXIT_SUCCESS);
	}

	if (target_fd < 0)
		goto eperm;

	ret = stat_statfs(dirname(target), &s1, &sfs1);
	if (ret)
		goto eperm;

	ret = fstat_fstatfs(target_fd, &s2, &sfs2);
	if (ret)
		goto eperm;

	if (!same_fsinfo(&s1, &s2, &sfs1, &sfs2))
		goto eperm;

	// basename() can modify its argument so accessing target_host is
	// invalid from now on.
	ret = mknodat(target_fd, basename(target_host), mode, dev);
	if (ret) {
		if (errno == EEXIST) {
			fprintf(stderr, "%d", errno);
			_exit(EXIT_FAILURE);
		}

		goto eperm;
	}

	_exit(EXIT_SUCCESS);

eperm:
	fprintf(stderr, "%d", EPERM);
	_exit(EXIT_FAILURE);
}
*/
import "C"

type cmdForkmknod struct {
	global *cmdGlobal
}

func (c *cmdForkmknod) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkmknod <PID> <path> <mode> <dev>"
	cmd.Short = "Perform mknod operations"
	cmd.Long = `Description:
  Perform mknod operations

  This set of internal commands are used for all seccom-based container mknod
  operations.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkmknod) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("This command should have been intercepted in cgo")
}
