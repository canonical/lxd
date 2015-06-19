/* scmtest
 *
 * Copyright © 2014 Canonical
 * Author: Serge Hallyn <serge.hallyn@ubuntu.com>
 *
 * This program is free software; you can redistribute it and/or modify
 * it under the terms of the GNU General Public License version 2, as
 * published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License along
 * with this program; if not, write to the Free Software Foundation, Inc.,
 * 51 Franklin Street, Fifth Floor, Boston, MA 02110-1301 USA.
 */

#define _GNU_SOURCE
#include <errno.h>
#include <stdio.h>
#include <stdbool.h>
#include <unistd.h>
#include <stdlib.h>
#include <stddef.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/param.h>
#include <dirent.h>
#include <fcntl.h>
#include <sys/types.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <sys/wait.h>

int sv[2];

/* These functions should be in a common file that contains helpers not
 * needing nih or dbus to compile.  They are copied from access_checks.c
 * and cgmanager-proxy.c
 */
void get_scm_creds_sync(int sock, struct ucred *cred)
{
	struct msghdr msg = { 0 };
	struct iovec iov;
	struct cmsghdr *cmsg;
	char cmsgbuf[CMSG_SPACE(sizeof(*cred))];
	char buf[1];
	int ret;
	int optval = 1;

	cred->pid = -1;
	cred->uid = -1;
	cred->gid = -1;

	if (setsockopt(sock, SOL_SOCKET, SO_PASSCRED, &optval, sizeof(optval)) == -1) {
		printf("Failed to set passcred: %s\n", strerror(errno));
		return;
	}
	buf[0] = '1';
	if (write(sock, buf, 1) != 1) {
		printf("Failed to start write on scm fd: %s\n", strerror(errno));
		return;
	}

	msg.msg_name = NULL;
	msg.msg_namelen = 0;
	msg.msg_control = cmsgbuf;
	msg.msg_controllen = sizeof(cmsgbuf);

	iov.iov_base = buf;
	iov.iov_len = sizeof(buf);
	msg.msg_iov = &iov;
	msg.msg_iovlen = 1;

	// retry logic is not ideal, especially as we are not
	// threaded.  Sleep at most 1 second waiting for the client
	// to send us the scm_cred
	ret = recvmsg(sock, &msg, 0);
	if (ret < 0) {
		printf("Failed to receive scm_cred: %s\n", strerror(errno));
		return;
	}

	cmsg = CMSG_FIRSTHDR(&msg);

	if (cmsg && cmsg->cmsg_len == CMSG_LEN(sizeof(struct ucred)) &&
			cmsg->cmsg_level == SOL_SOCKET &&
			cmsg->cmsg_type == SCM_CREDENTIALS) {
		memcpy(cred, CMSG_DATA(cmsg), sizeof(*cred));
	}
}

static void kick_fd_client(int fd)
{
	char buf = '1';
	if (write(fd, &buf, 1) != 1)
		exit(1);
}

int send_creds(int sock, struct ucred *cred)
{
	struct msghdr msg = { 0 };
	struct iovec iov;
	struct cmsghdr *cmsg;
	char cmsgbuf[CMSG_SPACE(sizeof(*cred))];
	char buf[1];
	buf[0] = 'p';

	msg.msg_control = cmsgbuf;
	msg.msg_controllen = sizeof(cmsgbuf);

	cmsg = CMSG_FIRSTHDR(&msg);
	cmsg->cmsg_len = CMSG_LEN(sizeof(struct ucred));
	cmsg->cmsg_level = SOL_SOCKET;
	cmsg->cmsg_type = SCM_CREDENTIALS;
	memcpy(CMSG_DATA(cmsg), cred, sizeof(*cred));

	msg.msg_name = NULL;
	msg.msg_namelen = 0;

	iov.iov_base = buf;
	iov.iov_len = sizeof(buf);
	msg.msg_iov = &iov;
	msg.msg_iovlen = 1;

	if (sendmsg(sock, &msg, 0) < 0) {
		printf("failed at sendmsg: %s\n", strerror(errno));
		return -1;
	}
	return 0;
}
static int proxyrecv(int sockfd, void *buf, size_t len)
{
	struct timeval tv;
	fd_set rfds;

	FD_ZERO(&rfds);
	FD_SET(sockfd, &rfds);
	tv.tv_sec = 2;
	tv.tv_usec = 0;

	if (select(sockfd+1, &rfds, NULL, NULL, &tv) < 0)
		return -1;
	return recv(sockfd, buf, len, MSG_DONTWAIT);
}

int wait_for_pid(pid_t pid)
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
	if (!WIFEXITED(status) || WEXITSTATUS(status) != 0)
		return -1;
	return 0;
}

int main()
{
	int optval = 1, pid;
	struct ucred mcred, rcred, vcred;
	char buf[1];

	if (socketpair(AF_UNIX, SOCK_DGRAM, 0, sv) < 0) {
		printf("Error creating socketpair: %s\n", strerror(errno));
		exit(1);
	}
	if (setsockopt(sv[1], SOL_SOCKET, SO_PASSCRED, &optval, sizeof(optval)) == -1) {
		printf("setsockopt: %s\n", strerror(errno));
		exit(1);
	}
	if (setsockopt(sv[0], SOL_SOCKET, SO_PASSCRED, &optval, sizeof(optval)) == -1) {
		printf("setsockopt: %s\n", strerror(errno));
		exit(1);
	}

	rcred.pid = -1;
	rcred.uid = -1;
	rcred.gid = -1;
	vcred.pid = -1;
	vcred.uid = -1;
	vcred.gid = -1;
	mcred.pid = getpid();
	mcred.uid = geteuid();
	mcred.gid = getegid();
	pid = fork();
	if (pid < 0)
		exit(1);
	if (!pid) {
		// receiver
		int i;
		for (i = 0; i < 2; i++) {
			kick_fd_client(sv[1]);
			get_scm_creds_sync(sv[1], &rcred);
			if (rcred.pid == -1) {
				printf("receiver: error receiving cred\n");
				exit(1);
			}
			if (rcred.uid != mcred.uid || rcred.pid != mcred.pid ||
				rcred.gid != mcred.gid) {
				printf("received a corrupted cred\n");
				exit(1);
			}
		}
		exit(0);
	}

	rcred.pid = getpid();
	rcred.uid = geteuid();
	rcred.gid = getegid();
	vcred.pid = getpid();
	vcred.uid = geteuid();
	vcred.gid = getegid();

	if (proxyrecv(sv[0], buf, 1) != 1) {
		printf("Error getting reply from server over socketpair\n");
		exit(2);
	}
	if (send_creds(sv[0], &rcred)) {
		printf("Error sending pid over SCM_CREDENTIAL\n");
		exit(2);
	}

	if (proxyrecv(sv[0], buf, 1) != 1) {
		printf("Error getting reply from server over socketpair\n");
		exit(2);
	}
	if (send_creds(sv[0], &vcred)) {
		printf("Error sending pid over SCM_CREDENTIAL\n");
		exit(2);
	}
	if (wait_for_pid(pid) < 0) {
		printf("Child exited with error\n");
		exit(3);
	}
	printf("PASS\n");
	exit(0);
}
