package main

import (
	"github.com/lxc/lxd/shared/logger"
)

/*
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <linux/types.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <sched.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

#include "../shared/netutils/netns_getifaddrs.c"
#include "include/memory_utils.h"
#include "include/namespaces.h"

bool netnsid_aware = false;
char errbuf[4096];

static int netns_set_nsid(int fd)
{
	__do_close_prot_errno int sockfd = -EBADF;
	int ret;
	char buf[NLMSG_ALIGN(sizeof(struct nlmsghdr)) +
		 NLMSG_ALIGN(sizeof(struct rtgenmsg)) +
		 NLMSG_ALIGN(1024)];
	struct nlmsghdr *hdr;
	struct rtgenmsg *msg;
	__s32 ns_id = -1;
	__u32 netns_fd = fd;

	sockfd = netlink_open(NETLINK_ROUTE);
	if (sockfd < 0)
		return -1;

	memset(buf, 0, sizeof(buf));
	hdr = (struct nlmsghdr *)buf;
	msg = (struct rtgenmsg *)NLMSG_DATA(hdr);

	hdr->nlmsg_len = NLMSG_LENGTH(sizeof(*msg));
	hdr->nlmsg_type = RTM_NEWNSID;
	hdr->nlmsg_flags = NLM_F_REQUEST | NLM_F_ACK;
	hdr->nlmsg_pid = 0;
	hdr->nlmsg_seq = RTM_NEWNSID;
	msg->rtgen_family = AF_UNSPEC;

	addattr(hdr, 1024, __LXC_NETNSA_FD, &netns_fd, sizeof(netns_fd));
	addattr(hdr, 1024, __LXC_NETNSA_NSID, &ns_id, sizeof(ns_id));

	ret = netlink_transaction(sockfd, hdr, hdr);
	if (ret < 0)
		return -1;

	return 0;
}

void is_netnsid_aware(int *hostnetns_fd, int *newnetns_fd)
{
	__do_close_prot_errno int sock_fd = -EBADF;
	int netnsid, ret;

	*hostnetns_fd = open("/proc/self/ns/net", O_RDONLY | O_CLOEXEC);
	if (*hostnetns_fd < 0) {
		(void)sprintf(errbuf, "%s", "Failed to preserve host network namespace");
		return;
	}

	ret = unshare(CLONE_NEWNET);
	if (ret < 0) {
		(void)sprintf(errbuf, "%s", "Failed to unshare network namespace");
		return;
	}

	*newnetns_fd = open("/proc/self/ns/net", O_RDONLY | O_CLOEXEC);
	if (*newnetns_fd < 0) {
		(void)sprintf(errbuf, "%s", "Failed to preserve new network namespace");
		return;
	}

	ret = netns_set_nsid(*hostnetns_fd);
	if (ret < 0) {
		(void)sprintf(errbuf, "%s", "failed to set network namespace identifier");
		return;
	}

	netnsid = netns_get_nsid(*hostnetns_fd);
	if (netnsid < 0) {
		(void)sprintf(errbuf, "%s", "Failed to get network namespace identifier");
		return;
	}

	sock_fd = socket(PF_NETLINK, SOCK_RAW | SOCK_CLOEXEC, NETLINK_ROUTE);
	if (sock_fd < 0) {
		(void)sprintf(errbuf, "%s", "Failed to open netlink routing socket");
		return;
	}

	ret = setsockopt(sock_fd, SOL_NETLINK, NETLINK_GET_STRICT_CHK, &(int){1}, sizeof(int));
	if (ret < 0) {
		// NETLINK_GET_STRICT_CHK isn't supported
		return;
	}

	// NETLINK_GET_STRICT_CHK is supported
	netnsid_aware = true;
}

void checkfeature()
{
	__do_close_prot_errno int hostnetns_fd = -EBADF, newnetns_fd = -EBADF;

	is_netnsid_aware(&hostnetns_fd, &newnetns_fd);

	if (setns(hostnetns_fd, CLONE_NEWNET) < 0)
		(void)sprintf(errbuf, "%s", "Failed to attach to host network namespace");

}

static bool is_empty_string(char *s)
{
	return (errbuf[0] == '\0');
}
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
import "C"

func CanUseNetnsGetifaddrs() bool {
	if !bool(C.is_empty_string(&C.errbuf[0])) {
		logger.Debugf("%s", C.GoString(&C.errbuf[0]))
	}

	return bool(C.netnsid_aware)
}
