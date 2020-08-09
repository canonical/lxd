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

#endif /* __LXD_SYSCALL_WRAPPER_H */
