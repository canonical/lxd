package main

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

#include "../shared/netns_getifaddrs.c"

bool netnsid_aware = false;

static int netns_set_nsid(int fd)
{
	int sockfd, ret;
	char buf[NLMSG_ALIGN(sizeof(struct nlmsghdr)) +
		 NLMSG_ALIGN(sizeof(struct rtgenmsg)) +
		 NLMSG_ALIGN(1024)];
	struct nlmsghdr *hdr;
	struct rtgenmsg *msg;
	int saved_errno;
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
	saved_errno = errno;
	close(sockfd);
	errno = saved_errno;
	if (ret < 0)
		return -1;

	return 0;
}

void checkfeature() {
	int netnsid, ret;
	struct netns_ifaddrs *ifaddrs;
	int hostnetns_fd = -1, newnetns_fd = -1;

	hostnetns_fd = open("/proc/self/ns/net", O_RDONLY | O_CLOEXEC);
	if (hostnetns_fd < 0) {
		fprintf(stderr, "Failed to preserve host network namespace\n");
		goto on_error;
	}

	ret = unshare(CLONE_NEWNET);
	if (ret < 0) {
		fprintf(stderr, "Failed to unshare network namespace\n");
		goto on_error;
	}

	newnetns_fd = open("/proc/self/ns/net", O_RDONLY | O_CLOEXEC);
	if (newnetns_fd < 0) {
		fprintf(stderr, "Failed to preserve new network namespace\n");
		goto on_error;
	}

	ret = setns(hostnetns_fd, CLONE_NEWNET);
	if (ret < 0) {
		fprintf(stderr, "Failed to attach to host network namespace\n");
		goto on_error;
	}

	ret = netns_set_nsid(newnetns_fd);
	if (ret < 0) {
		fprintf(stderr, "failed to set network namespace identifier\n");
		goto on_error;
	}

	netnsid = netns_get_nsid(newnetns_fd);
	if (netnsid < 0) {
		fprintf(stderr, "Failed to get network namespace identifier\n");
		goto on_error;
	}

	ret = netns_getifaddrs(&ifaddrs, netnsid, &netnsid_aware);
	netns_freeifaddrs(ifaddrs);
	if (ret < 0)
		fprintf(stderr, "Netlink is not fully network namespace id aware\n");

on_error:
	if (hostnetns_fd >= 0)
		close(hostnetns_fd);

	if (newnetns_fd >= 0)
		close(newnetns_fd);
}
*/
import "C"

func CanUseNetnsGetifaddrs() bool {
	return bool(C.netnsid_aware)
}
