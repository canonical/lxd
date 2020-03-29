package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

/*
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>
#include <limits.h>

#include "include/memory_utils.h"
#include <lxc/attach_options.h>
#include <lxc/lxccontainer.h>

extern char *advance_arg(bool required);

static bool write_nointr(int fd, const void *buf, size_t count)
{
	ssize_t ret;

	do {
		ret = write(fd, buf, count);
	} while (ret < 0 && errno == EINTR);

	if (ret < 0)
		return false;

	return (size_t)ret == count;
}

define_cleanup_function(struct lxc_container *, lxc_container_put);

static int wait_for_pid_status_nointr(pid_t pid)
{
	int status, ret;

again:
	ret = waitpid(pid, &status, 0);
	if (ret == -1) {
		if (errno == EINTR)
			goto again;

		return -1;
	}

	if (ret != pid)
		goto again;

	return status;
}

static char *must_copy_string(const char *entry)
{
	char *ret;

	if (!entry)
		return NULL;

	do {
		ret = strdup(entry);
	} while (!ret);

	return ret;
}

static void *must_realloc(void *orig, size_t sz)
{
	void *ret;

	do {
		ret = realloc(orig, sz);
	} while (!ret);

	return ret;
}

static int append_null_to_list(void ***list)
{
	int newentry = 0;

	if (*list)
		for (; (*list)[newentry]; newentry++)
			;

	*list = must_realloc(*list, (newentry + 2) * sizeof(void **));
	(*list)[newentry + 1] = NULL;
	return newentry;
}

static void must_append_string(char ***list, char *entry)
{
	int newentry;
	char *copy;

	newentry = append_null_to_list((void ***)list);
	copy = must_copy_string(entry);
	(*list)[newentry] = copy;
}

// We use a separate function because cleanup macros are called during stack
// unwinding if I'm not mistaken and if the compiler knows it exits it won't
// call them. That's not a problem since we're exiting but I just like to be on
// the safe side in case we ever call this from a different context. We also
// tell the compiler to not inline us.
__attribute__ ((noinline)) static int __forkexec(void)
{
	__do_close int status_pipe = 6;
	__do_free_string_list char **argvp = NULL, **envvp = NULL;
	call_cleaner(lxc_container_put) struct lxc_container *c = NULL;
	const char *config_path = NULL, *lxcpath = NULL, *name = NULL;
	char *cwd = NULL;
	lxc_attach_options_t attach_options = LXC_ATTACH_OPTIONS_DEFAULT;
	lxc_attach_command_t command = {
		.program = NULL,
	};
	int ret;
	pid_t pid;
	uid_t uid;
	gid_t gid;

	if (geteuid() != 0) {
		fprintf(stderr, "Error: forkexec requires root privileges\n");
		return EXIT_FAILURE;
	}

	name = advance_arg(false);
	if (name == NULL ||
	    (strcmp(name, "--help") == 0 ||
	     strcmp(name, "--version") == 0 || strcmp(name, "-h") == 0))
		return 0;

	lxcpath = advance_arg(true);
	config_path = advance_arg(true);
	cwd = advance_arg(true);
	uid = atoi(advance_arg(true));
	if (uid < 0)
		uid = (uid_t) - 1;
	gid = atoi(advance_arg(true));
	if (gid < 0)
		gid = (gid_t) - 1;

	for (char *arg = NULL, *section = NULL; (arg = advance_arg(false)); ) {
		if (!strcmp(arg, "--") && (!section || strcmp(section, "cmd"))) {
			section = NULL;
			continue;
		}

		if (!section) {
			section = arg;
			continue;
		}

		if (!strcmp(section, "env")) {
			if (!strncmp(arg, "HOME=", STRLITERALLEN("HOME=")))
				attach_options.initial_cwd = arg + STRLITERALLEN("HOME=");
			must_append_string(&envvp, arg);
		} else if (!strcmp(section, "cmd")) {
			must_append_string(&argvp, arg);
		} else {
			fprintf(stderr, "Invalid exec section %s\n", section);
			return EXIT_FAILURE;
		}
	}

	if (!argvp || !*argvp) {
		fprintf(stderr, "No command specified\n");
		return EXIT_FAILURE;
	}

	c = lxc_container_new(name, lxcpath);
	if (!c)
		return EXIT_FAILURE;

	c->clear_config(c);

	if (!c->load_config(c, config_path))
		return EXIT_FAILURE;

	if (strcmp(cwd, ""))
		attach_options.initial_cwd = cwd;
	attach_options.env_policy = LXC_ATTACH_CLEAR_ENV;
	attach_options.extra_env_vars = envvp;
	attach_options.stdin_fd = 3;
	attach_options.stdout_fd = 4;
	attach_options.stderr_fd = 5;
	attach_options.uid = uid;
	attach_options.gid = gid;
	command.program = argvp[0];
	command.argv = argvp;

	ret = c->attach(c, lxc_attach_run_command, &command, &attach_options, &pid);
	if (ret < 0)
		return EXIT_FAILURE;

	if (!write_nointr(status_pipe, &pid, sizeof(pid))) {
		// Kill the child just to be safe.
		fprintf(stderr, "Failed to send pid %d of executing child to LXD. Killing child\n", pid);
		kill(pid, SIGKILL);
	}

	ret = wait_for_pid_status_nointr(pid);
	if (ret < 0)
		return EXIT_FAILURE;

	if (WIFEXITED(ret))
		return WEXITSTATUS(ret);

	if (WIFSIGNALED(ret))
		return 128 + WTERMSIG(ret);

	return EXIT_FAILURE;
}

void forkexec(void)
{
	_exit(__forkexec());
}
*/
import "C"

type cmdForkexec struct {
	global *cmdGlobal
}

func (c *cmdForkexec) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkexec <container name> <containers path> <config> <cwd> <uid> <gid> -- env [key=value...] -- cmd <args...>"
	cmd.Short = "Execute a task inside the container"
	cmd.Long = `Description:
  Execute a task inside the container

  This internal command is used to spawn a task inside the container and
  allow LXD to interact with it.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkexec) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("This command should have been intercepted in cgo")
}
