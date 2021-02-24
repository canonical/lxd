// +build linux
// +build cgo

#ifndef LXD_UNIXFD_H
#define LXD_UNIXFD_H

#include <linux/types.h>
#include <sys/socket.h>
#include <sys/types.h>

/*
 * Technically 253 is the kernel limit but we want to the struct to be a
 * multiple of 8.
 */
#define KERNEL_SCM_MAX_FD 252

struct unix_fds {
	__u32 fd_count_max;
	__u32 fd_count_ret;
	__s32 fd[KERNEL_SCM_MAX_FD];
} __attribute__((aligned(8)));

extern int lxc_abstract_unix_send_fds(int fd, int *sendfds, int num_sendfds,
				      void *data, size_t size);

extern ssize_t lxc_abstract_unix_recv_fds_iov(int fd, struct unix_fds *ret_fds,
					      struct iovec *ret_iov,
					      size_t size_ret_iov);

extern ssize_t lxc_abstract_unix_recv_fds(int fd, struct unix_fds *ret_fds,
					  void *ret_data, size_t size_ret_data);

#endif // LXD_UNIXFD_H
