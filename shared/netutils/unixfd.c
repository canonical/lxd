// +build none

#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <grp.h>
#include <limits.h>
#include <poll.h>
#include <pty.h>
#include <pwd.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/stat.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <sys/un.h>

#include "../../lxd/include/memory_utils.h"

int lxc_abstract_unix_send_fds(int fd, int *sendfds, int num_sendfds,
			       void *data, size_t size)
{
	__do_free char *cmsgbuf = NULL;
	struct msghdr msg;
	struct iovec iov;
	struct cmsghdr *cmsg = NULL;
	char buf[1] = {0};
	size_t cmsgbufsize = CMSG_SPACE(num_sendfds * sizeof(int));

	memset(&msg, 0, sizeof(msg));
	memset(&iov, 0, sizeof(iov));

	cmsgbuf = malloc(cmsgbufsize);
	if (!cmsgbuf)
		return -1;

	msg.msg_control = cmsgbuf;
	msg.msg_controllen = cmsgbufsize;

	cmsg = CMSG_FIRSTHDR(&msg);
	cmsg->cmsg_level = SOL_SOCKET;
	cmsg->cmsg_type = SCM_RIGHTS;
	cmsg->cmsg_len = CMSG_LEN(num_sendfds * sizeof(int));

	msg.msg_controllen = cmsg->cmsg_len;

	memcpy(CMSG_DATA(cmsg), sendfds, num_sendfds * sizeof(int));

	iov.iov_base = data ? data : buf;
	iov.iov_len = data ? size : sizeof(buf);
	msg.msg_iov = &iov;
	msg.msg_iovlen = 1;

	return sendmsg(fd, &msg, MSG_NOSIGNAL);
}

static ssize_t lxc_abstract_unix_recv_fds_iov(int fd, int *recvfds,
					      int num_recvfds,
					      struct iovec *iov, size_t iovlen)
{
	__do_free char *cmsgbuf = NULL;
	ssize_t ret;
	struct msghdr msg = { 0 };
	struct cmsghdr *cmsg = NULL;
	size_t cmsgbufsize = CMSG_SPACE(sizeof(struct ucred)) +
			     CMSG_SPACE(num_recvfds * sizeof(int));

	cmsgbuf = malloc(cmsgbufsize);
	if (!cmsgbuf) {
		errno = ENOMEM;
		return -1;
	}

	msg.msg_control = cmsgbuf;
	msg.msg_controllen = cmsgbufsize;

	msg.msg_iov = iov;
	msg.msg_iovlen = iovlen;

again:
	ret = recvmsg(fd, &msg, MSG_TRUNC | MSG_CMSG_CLOEXEC | MSG_NOSIGNAL);
	if (ret < 0) {
		if (errno == EINTR)
			goto again;

		goto out;
	}
	if (ret == 0)
		goto out;

	// If SO_PASSCRED is set we will always get a ucred message.
	for (cmsg = CMSG_FIRSTHDR(&msg); cmsg; cmsg = CMSG_NXTHDR(&msg, cmsg)) {
		if (cmsg->cmsg_type != SCM_RIGHTS)
			continue;

		memset(recvfds, -1, num_recvfds * sizeof(int));
		if (cmsg &&
		    cmsg->cmsg_len == CMSG_LEN(num_recvfds * sizeof(int)) &&
		    cmsg->cmsg_level == SOL_SOCKET)
			memcpy(recvfds, CMSG_DATA(cmsg), num_recvfds * sizeof(int));
		break;
	}

out:
	return ret;
}

static ssize_t lxc_abstract_unix_recv_fds(int fd, int *recvfds, int num_recvfds,
				   void *data, size_t size)
{
	char buf[1] = {0};
	struct iovec iov = {
		.iov_base = data ? data : buf,
		.iov_len = data ? size : sizeof(buf),
	};
	return lxc_abstract_unix_recv_fds_iov(fd, recvfds, num_recvfds, &iov, iov.iov_len);
}
