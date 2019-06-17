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

static int __do_chowmknod(const char *path, mode_t mode, dev_t dev,
			  uid_t uid, gid_t gid)
{
	int ret;

	ret = mknodat(AT_FDCWD, path, mode, dev);
	if (ret)
		return -1;

	return chown(path, uid, gid);
}

static int __do_chdirchroot(const char *cwd, const char *root)
{
	if (cwd && chdir(cwd))
		return -1;

	return chroot(root);
}

static inline bool same_fsinfo(struct stat *s1, struct stat *s2,
			       struct statfs *sfs1, struct statfs *sfs2)
{
	return ((sfs1->f_type == sfs2->f_type) && (s1->st_dev == s2->st_dev) && (s1->st_ino == s2->st_ino));
}

// Expects command line to be in the form:
// <PID> <root-uid> <root-gid> <path> <mode> <dev>
void forkmknod()
{
	__do_close_prot_errno int target_fd = -EBADF;
	__do_free char *p1 = NULL, *p2 = NULL;
	int ret;
	ssize_t bytes = 0;
	char *cur = NULL, *path = NULL, *rootfs_path = NULL,
		*dir_name_ct = NULL, *dir_name_host = NULL, *base_name = NULL;
	mode_t mode = 0;
	dev_t dev = 0;
	pid_t pid = 0;
	uid_t uid = -1;
	gid_t gid = -1;
	char cwd[256], root[256], cwd_path[PATH_MAX];
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

	// Get the container PID
	pid = atoi(cur);

	// path to create
	path = advance_arg(true);
	mode = atoi(advance_arg(true));
	dev = atoi(advance_arg(true));

	uid = get_root_uid(pid);
	if (uid < 0)
		fprintf(stderr, "No root uid found (%d)\n", uid);

	gid = get_root_gid(pid);
	if (gid < 0)
		fprintf(stderr, "No root gid found (%d)\n", gid);

	rootfs_path = advance_arg(true);
	if (*rootfs_path) {
		// dirname() can modify its argument
		p1 = strdup(rootfs_path);
		if (!p1)
			_exit(EXIT_FAILURE);

		target_fd = open(dirname(p1), O_PATH | O_RDONLY | O_CLOEXEC);
	}

	snprintf(cwd, sizeof(cwd), "/proc/%d/cwd", pid);
	snprintf(root, sizeof(root), "/proc/%d/root", pid);
	ret = __do_chdirchroot(cwd, root);
	if (ret)
		goto eperm;

	ret = __do_chowmknod(path, mode, dev, uid, gid);
	if (ret) {
		if (errno == EEXIST) {
			fprintf(stderr, "%d", errno);
			_exit(EXIT_FAILURE);
		}
	} else {
		_exit(EXIT_SUCCESS);
	}

	if (!*rootfs_path || target_fd < 0)
		goto eperm;

	dir_name_ct = dirname(path);
	ret = stat(dir_name_ct, &s1);
	if (ret)
		goto eperm;

	ret = statfs(dir_name_ct, &sfs1);
	if (ret)
		goto eperm;

	ret = fstat(target_fd, &s2);
	if (ret)
		goto eperm;

	ret = fstatfs(target_fd, &sfs2);
	if (ret)
		goto eperm;

	if (!same_fsinfo(&s1, &s2, &sfs1, &sfs2))
		goto eperm;

	// basename() can modify its argument
	p2 = strdup(rootfs_path);
	if (!p2)
		goto eperm;

	base_name = basename(p2);
	ret = mknodat(target_fd, base_name, mode, dev);
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
