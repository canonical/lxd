// +build linux
// +build cgo
//
#ifndef LXD_UNIXFD_H
#define LXD_UNIXFD_H

#include <sys/socket.h>
#include <sys/types.h>

extern int lxc_abstract_unix_send_fds(int fd, int *sendfds, int num_sendfds,
				      void *data, size_t size);

extern ssize_t lxc_abstract_unix_recv_fds_iov(int fd, int *recvfds,
					      int num_recvfds,
					      struct iovec *iov, size_t iovlen);

extern ssize_t lxc_abstract_unix_recv_fds(int fd, int *recvfds, int num_recvfds,
					  void *data, size_t size);

#endif // LXD_UNIXFD_H
