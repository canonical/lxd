package main

import (
	"github.com/spf13/cobra"
)

/*

#define _GNU_SOURCE
#include <asm/types.h>
#include <errno.h>
#include <fcntl.h>
#include <linux/netlink.h>
#include <linux/rtnetlink.h>
#include <sched.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

#include "../shared/network.c"

#ifndef UEVENT_SEND
#define UEVENT_SEND 16
#endif

extern char *advance_arg(bool required);
extern void attach_userns(int pid);
extern int dosetns(int pid, char *nstype);

struct nlmsg {
	struct nlmsghdr *nlmsghdr;
	ssize_t cap;
};

static struct nlmsg *nlmsg_alloc(size_t size)
{
	struct nlmsg *nlmsg;
	size_t len = NLMSG_HDRLEN + NLMSG_ALIGN(size);

	nlmsg = (struct nlmsg *)malloc(sizeof(struct nlmsg));
	if (!nlmsg)
		return NULL;

	nlmsg->nlmsghdr = (struct nlmsghdr *)malloc(len);
	if (!nlmsg->nlmsghdr)
		goto errout;

	memset(nlmsg->nlmsghdr, 0, len);
	nlmsg->cap = len;
	nlmsg->nlmsghdr->nlmsg_len = NLMSG_HDRLEN;

	return nlmsg;
errout:
	free(nlmsg);
	return NULL;
}

static void *nlmsg_reserve_unaligned(struct nlmsg *nlmsg, size_t len)
{
	char *buf;
	size_t nlmsg_len = nlmsg->nlmsghdr->nlmsg_len;
	size_t tlen = len;

	if ((ssize_t)(nlmsg_len + tlen) > nlmsg->cap)
		return NULL;

	buf = ((char *)(nlmsg->nlmsghdr)) + nlmsg_len;
	nlmsg->nlmsghdr->nlmsg_len += tlen;

	if (tlen > len)
		memset(buf + len, 0, tlen - len);

	return buf;
}

int can_inject_uevent(const char *uevent, size_t len)
{
	int ret, sock_fd;
	char *umsg = NULL;
	struct nlmsg *nlmsg = NULL;

	sock_fd = netlink_open(NETLINK_KOBJECT_UEVENT);
	if (sock_fd < 0) {
		return -1;
	}

	nlmsg = nlmsg_alloc(len);
	if (!nlmsg) {
		ret = -1;
		goto on_error;
	}

	nlmsg->nlmsghdr->nlmsg_flags = NLM_F_REQUEST;
	nlmsg->nlmsghdr->nlmsg_type = UEVENT_SEND;
	nlmsg->nlmsghdr->nlmsg_pid = 0;

	umsg = nlmsg_reserve_unaligned(nlmsg, len);
	if (!umsg) {
		ret = -1;
		goto on_error;
	}

	memcpy(umsg, uevent, len);

	ret = __netlink_send(sock_fd, nlmsg->nlmsghdr);
	if (ret < 0) {
		ret = -1;
		goto on_error;
	}

	ret = 0;

on_error:
	close(sock_fd);
	free(nlmsg);
	return ret;
}

static int inject_uevent(const char *uevent, size_t len)
{
	int ret, sock_fd;
	char *umsg = NULL;
	struct nlmsg *nlmsg = NULL;

	sock_fd = netlink_open(NETLINK_KOBJECT_UEVENT);
	if (sock_fd < 0) {
		return -1;
	}

	nlmsg = nlmsg_alloc(len);
	if (!nlmsg) {
		ret = -1;
		goto on_error;
	}

	nlmsg->nlmsghdr->nlmsg_flags = NLM_F_ACK | NLM_F_REQUEST;
	nlmsg->nlmsghdr->nlmsg_type = UEVENT_SEND;
	nlmsg->nlmsghdr->nlmsg_pid = 0;

	umsg = nlmsg_reserve_unaligned(nlmsg, len);
	if (!umsg) {
		ret = -1;
		goto on_error;
	}

	memcpy(umsg, uevent, len);

	ret = netlink_transaction(sock_fd, nlmsg->nlmsghdr, nlmsg->nlmsghdr);
	if (ret < 0) {
		ret = -1;
		goto on_error;
	}

	ret = 0;

on_error:
	close(sock_fd);
	free(nlmsg);
	return ret;
}

void forkuevent() {
	char *uevent = NULL;
	char *cur = NULL;
	pid_t pid = 0;
	size_t len = 0;

	cur = advance_arg(false);
	if (cur == NULL || (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0)) {
		fprintf(stderr, "Error: Missing PID\n");
		_exit(1);
	}

	// Get the pid
	cur = advance_arg(false);
	if (cur == NULL || (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0)) {
		fprintf(stderr, "Error: Missing PID\n");
		_exit(1);
	}
	pid = atoi(cur);

	// Get the size
	cur = advance_arg(false);
	if (cur == NULL || (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0)) {
		fprintf(stderr, "Error: Missing uevent length\n");
		_exit(1);
	}
	len = atoi(cur);

	// Get the uevent
	cur = advance_arg(false);
	if (cur == NULL || (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0)) {
		fprintf(stderr, "Error: Missing uevent\n");
		_exit(1);
	}
	uevent = cur;

	// Check that we're root
	if (geteuid() != 0) {
		fprintf(stderr, "Error: forkuevent requires root privileges\n");
		_exit(1);
	}

	attach_userns(pid);

	if (dosetns(pid, "net") < 0) {
		fprintf(stderr, "Failed to setns to container network namespace: %s\n", strerror(errno));
		_exit(1);
	}

	if (inject_uevent(uevent, len) < 0) {
		fprintf(stderr, "Failed to inject uevent\n");
		_exit(1);
	}
}
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
import "C"

type cmdForkuevent struct {
	global *cmdGlobal
}

func (c *cmdForkuevent) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkuevent"
	cmd.Short = "Inject uevents into container's network namespace"
	cmd.Long = `Description:
  Inject uevent into a container's network namespace

  This internal command is used to inject uevents into unprivileged container's
  network namespaces.
`
	cmd.Hidden = true

	// pull
	cmdInject := &cobra.Command{}
	cmdInject.Use = "inject <PID> <len> <uevent>"
	cmdInject.Args = cobra.ExactArgs(3)
	cmdInject.RunE = c.Run
	cmd.AddCommand(cmdInject)

	return cmd
}

func (c *cmdForkuevent) Run(cmd *cobra.Command, args []string) error {
	return nil
}
