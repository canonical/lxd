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
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/stat.h>
#include <sys/types.h>
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

// Expects command line to be in the form:
// <PID> <root-uid> <root-gid> <path> <mode> <dev>
void forkmknod()
{
	ssize_t bytes = 0;
	char *cur = NULL;
	char *path = NULL;
	mode_t mode = 0;
	dev_t dev = 0;
	pid_t pid = 0;
	uid_t uid = -1;
	gid_t gid = -1;
	char cwd[256], cwd_path[PATH_MAX];

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
	if (!path)
		_exit(EXIT_FAILURE);

	mode = atoi(advance_arg(true));
	dev = atoi(advance_arg(true));

	uid = get_root_uid(pid);
	if (uid < 0)
		fprintf(stderr, "No root uid found (%d)\n", uid);

	gid = get_root_gid(pid);
	if (gid < 0)
		fprintf(stderr, "No root gid found (%d)\n", gid);

	snprintf(cwd, sizeof(cwd), "/proc/%d/cwd", pid);
	if (chdir(cwd)) {
		fprintf(stderr, "%d", errno);
		_exit(EXIT_FAILURE);
	}

	snprintf(cwd, sizeof(cwd), "/proc/%d/root", pid);
	if (chroot(cwd)) {
		fprintf(stderr, "%d", errno);
		_exit(EXIT_FAILURE);
	}

	if (mknod(path, mode, dev)) {
		fprintf(stderr, "%d", errno);
		_exit(EXIT_FAILURE);
	}

	if (chown(path, uid, gid)) {
		fprintf(stderr, "%d", errno);
		_exit(EXIT_FAILURE);
	}

	_exit(EXIT_SUCCESS);
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
