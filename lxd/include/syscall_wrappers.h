#ifndef __LXD_SYSCALL_WRAPPER_H
#define __LXD_SYSCALL_WRAPPER_H

#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <asm/unistd.h>
#include <errno.h>
#include <sys/syscall.h>
#include <sys/types.h>
#include <unistd.h>

#include "syscall_numbers.h"

#ifndef CLOSE_RANGE_UNSHARE
#define CLOSE_RANGE_UNSHARE (1U << 1)
#endif

static inline int close_range(unsigned int fd, unsigned int max_fd, unsigned int flags)
{
	return syscall(__NR_close_range, fd, max_fd, flags);
}

static inline int open_tree(int dfd, const char *filename, unsigned int flags)
{
	return syscall(__NR_open_tree, dfd, filename, flags);
}

/*
 * mount_setattr()
 */
struct lxc_mount_attr {
	__u64 attr_set;
	__u64 attr_clr;
	__u64 propagation;
	__u64 userns_fd;
};

static inline int mount_setattr(int dfd, const char *path, unsigned int flags,
				struct lxc_mount_attr *attr, size_t size)
{
	return syscall(__NR_mount_setattr, dfd, path, flags, attr, size);
}

#endif /* __LXD_SYSCALL_WRAPPER_H */
