/*------------------------------------------------------------------------------
|
|                                   C Module
|
|-------------------------------------------------------------------------------
|
| Filename   : unixfd.h
| Description: Unix domain socket file descriptor passing interface
|              Defines data structures, constants, and function declarations
|              for sending and receiving file descriptors over Unix domain
|              sockets. Provides control flags for specifying expected file
|              descriptor counts (exact, less, more, or none) and status flags
|              indicating the actual received count. Declares the public API
|              for flexible inter-process file descriptor exchange via
|              abstract namespace sockets.
|
| Copyright  : Copyright (C) Canonical Ltd.
|
| This program is free software: you can redistribute it and/or modify
| it under the terms of the GNU Affero General Public License as
| published by the Free Software Foundation, either version 3 of the
| License, or (at your option) any later version.
|
| This program is distributed in the hope that it will be useful,
| but WITHOUT ANY WARRANTY; without even the implied warranty of
| MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
| GNU Affero General Public License for more details.
|
| You should have received a copy of the GNU Affero General Public License
| along with this program.  If not, see <https://www.gnu.org/licenses/>.
|-------------------------------------------------------------------------------
*/

#ifndef LXD_UNIXFD_H
#define LXD_UNIXFD_H

#include <linux/types.h>
#include <sys/socket.h>
#include <sys/types.h>

#define KERNEL_SCM_MAX_FD 253

/* Allow the caller to set expectations. */
#define UNIX_FDS_ACCEPT_EXACT	((__u32)(1 << 0)) /* default */
#define UNIX_FDS_ACCEPT_LESS	((__u32)(1 << 1))
#define UNIX_FDS_ACCEPT_MORE	((__u32)(1 << 2)) /* wipe any extra fds */
#define UNIX_FDS_ACCEPT_NONE	((__u32)(1 << 3))
#define UNIX_FDS_ACCEPT_MASK (UNIX_FDS_ACCEPT_EXACT | UNIX_FDS_ACCEPT_LESS | UNIX_FDS_ACCEPT_MORE | UNIX_FDS_ACCEPT_NONE)

/* Allow the callee to disappoint them. */
#define UNIX_FDS_RECEIVED_EXACT	((__u32)(1 << 16))
#define UNIX_FDS_RECEIVED_LESS	((__u32)(1 << 17))
#define UNIX_FDS_RECEIVED_MORE	((__u32)(1 << 18))
#define UNIX_FDS_RECEIVED_NONE	((__u32)(1 << 19))

struct unix_fds {
	__u32 fd_count_max;
	__u32 fd_count_ret;
	__u32 flags;
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
