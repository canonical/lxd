package main

import (
	"fmt"

	"github.com/spf13/cobra"

	// Used by cgo
	_ "github.com/lxc/lxd/lxd/include"
)

/*
#include "config.h"

#include <ctype.h>
#include <errno.h>
#include <fcntl.h>
#include <grp.h>
#include <sched.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/fsuid.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/vfs.h>
#include <unistd.h>

#include "lxd.h"
#include "error_utils.h"
#include "file_utils.h"
#include "macro.h"
#include "memory_utils.h"
#include "mount_utils.h"
#include "process_utils.h"
#include "syscall_numbers.h"
#include "syscall_wrappers.h"

#ifndef PIPEFS_MAGIC
#define PIPEFS_MAGIC 0x50495045
#endif

#define FD_PIPE_UIDMAP 3
#define FD_PIPE_GIDMAP 4

#define MAP_SIZE ((ssize_t)4096)

typedef __typeof__(((struct statfs *)NULL)->f_type) fs_type_magic;
static bool is_fs_type(const struct statfs *fs, fs_type_magic magic_val)
{
	return (fs->f_type == (fs_type_magic)magic_val);
}

static bool fhas_fs_type(int fd, fs_type_magic magic_val)
{
	int ret;
	struct statfs sb;

	ret = fstatfs(fd, &sb);
	if (ret < 0)
		return false;

	return is_fs_type(&sb, magic_val);
}

static inline bool drop_groups(void)
{
	return setgroups(0, NULL) == 0;
}

static inline bool switch_resids(uid_t uid, gid_t gid)
{
	if (setresgid(gid, gid, gid))
		return false;

	if (setresuid(uid, uid, uid))
		return false;

	if (setfsgid(-1) != gid)
		return false;

	if (setfsuid(-1) != uid)
		return false;

	return true;
}

static int write_id_mapping(bool is_uid)
{
	__do_close int fd_idmap = -EBADF;
	ssize_t bytes_read, ret;
	char buf[PROC_PID_IDMAP_LEN], idmap[LXC_IDMAPLEN];

	if (is_uid)
		ret = strnprintf(buf, sizeof(buf), "/proc/%d/uid_map", getppid());
	else
		ret = strnprintf(buf, sizeof(buf), "/proc/%d/gid_map", getppid());
	if (ret < 0)
		return -errno;

	fd_idmap = open(buf, O_WRONLY | O_CLOEXEC | O_NOCTTY | O_NOFOLLOW);
	if (fd_idmap < 0)
		return -1;

	if (is_uid)
		bytes_read = read_nointr(FD_PIPE_UIDMAP, idmap, sizeof(idmap));
	else
		bytes_read = read_nointr(FD_PIPE_GIDMAP, idmap, sizeof(idmap));
	if (bytes_read < 0 || bytes_read >= sizeof(idmap))
		return -errno;

	ret = write_nointr(fd_idmap, idmap, bytes_read);
	if (ret < 0 || ret != bytes_read)
		return -errno;

	return 0;
}

static int write_id_mappings(void)
{
	int ret;

	ret = write_id_mapping(true);
	if (!ret)
		ret = write_id_mapping(false);
	return ret;
}

static int safe_uint(const char *numstr, unsigned int *converted)
{
	char *err = NULL;
	unsigned long int uli;

	while (isspace(*numstr))
		numstr++;

	if (*numstr == '-')
		return -EINVAL;

	errno = 0;
	uli = strtoul(numstr, &err, 0);
	if (errno == ERANGE && uli == ULONG_MAX)
		return -ERANGE;

	if (err == numstr || *err != '\0')
		return -EINVAL;

	if (uli > UINT_MAX)
		return -ERANGE;

	*converted = (unsigned int)uli;
	return 0;
}

__attribute__ ((noinline)) int __forkusernsexec(void)
{
	__do_free_string_list char **argvp = NULL;
	pid_t pid;
	ssize_t ret;
	int fd_socket[2];
	char c;
	char *cur;
	unsigned int keep_fd_up_to = STDERR_FILENO;

	if (geteuid() != 0)
		return log_error(EXIT_FAILURE, "Error: forkexec requires root privileges");

	cur = advance_arg(false);
	if (!cur || (strcmp(cur, "--help") == 0 ||
	     strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0))
		return EXIT_SUCCESS;

	if (strncmp(cur, "--keep-fd-up-to=", STRLITERALLEN("--keep-fd-up-to=")) == 0) {
		cur += STRLITERALLEN("--keep-fd-up-to=");
		ret = safe_uint(cur, &keep_fd_up_to);
		if (ret)
			return log_error(EXIT_FAILURE, "Invalid fd number %s specified", cur);

		cur = advance_arg(true);
	}

	if (!fhas_fs_type(FD_PIPE_UIDMAP, PIPEFS_MAGIC))
		return log_error(EXIT_FAILURE, "Error: Missing UID map FD");

	if (!fhas_fs_type(FD_PIPE_GIDMAP, PIPEFS_MAGIC))
		return log_error(EXIT_FAILURE, "Error: Missing GID map FD");

	ret = socketpair(AF_LOCAL, SOCK_STREAM | SOCK_CLOEXEC, 0, fd_socket);
	if (ret < 0)
		return EXIT_FAILURE;

	pid = fork();
	if (pid < 0)
		return EXIT_FAILURE;

	if (pid == 0) {
		close_prot_errno_disarm(fd_socket[0]);

		ret = read_nointr(fd_socket[1], &c, 1);
		if (ret != 1)
			_exit(EXIT_FAILURE);

		ret = write_id_mappings();
		if (ret < 0)
			_exit(EXIT_FAILURE);

		ret = write_nointr(fd_socket[1], &c, 1);
		if (ret != 1)
			_exit(EXIT_FAILURE);

		_exit(EXIT_SUCCESS);
	}

	close_prot_errno_disarm(fd_socket[1]);

	ret = unshare(CLONE_NEWUSER);
	if (ret) {
		kill(pid, SIGKILL);
		goto out_reap;
	}

	ret = write_nointr(fd_socket[0], &c, 1);
	if (ret == 1)
		ret = read_nointr(fd_socket[0], &c, 1);
	if (ret != 1) {
		kill(pid, SIGKILL);
		goto out_reap;
	}

	if (!drop_groups()) {
		kill(pid, SIGKILL);
		goto out_reap;
	}

	if (!switch_resids(0, 0)) {
		kill(pid, SIGKILL);
		goto out_reap;
	}

	close_prot_errno_disarm(fd_socket[0]);

out_reap:
	ret = wait_for_pid(pid);
	if (ret)
		return EXIT_FAILURE;

	for (; cur; cur = advance_arg(false)) {
		ret = push_vargs(&argvp, cur);
		if (ret < 0)
			return log_error(EXIT_FAILURE, "Failed to add %s to arg array", cur);
	}

	if (!argvp || !*argvp)
		return log_error(EXIT_FAILURE, "No command specified");

	ret = lxd_close_range(keep_fd_up_to + 1, UINT_MAX, CLOSE_RANGE_CLOEXEC);
	if (ret && !IN_SET(errno, ENOSYS, EINVAL))
		return log_error(EXIT_FAILURE, "Aborting forkusernsexec to prevent leaking file descriptors");

	execvp(argvp[0], argvp);
	return EXIT_FAILURE;
}

void forkusernsexec(void)
{
	_exit(__forkusernsexec());
}
*/
import "C"

type cmdForkusernsexec struct {
	global     *cmdGlobal
	keepFdUpTo int
}

func (c *cmdForkusernsexec) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkusernsexec <cmd> <args>"
	cmd.Short = "Run command in user namespace"
	cmd.Flags().IntVar(&c.keepFdUpTo, "keep-fd-up-to", (1 << 31), fmt.Sprintf("Keep all fds below and including this one and close all the ones above it"))
	cmd.Long = `Description:
  Run command in user namespace

  This command is used to execute a program in a new user namespace.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkusernsexec) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("This command should have been intercepted in cgo")
}
