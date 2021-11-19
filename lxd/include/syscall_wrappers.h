#ifndef __LXD_SYSCALL_WRAPPER_H
#define __LXD_SYSCALL_WRAPPER_H

#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <asm/unistd.h>
#include <errno.h>
#include <linux/kcmp.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <sys/types.h>
#include <unistd.h>

#include "syscall_numbers.h"

#ifndef CLOSE_RANGE_UNSHARE
#define CLOSE_RANGE_UNSHARE (1U << 1)
#endif

#ifndef CLOSE_RANGE_CLOEXEC
#define CLOSE_RANGE_CLOEXEC (1U << 2)
#endif

static inline int lxd_close_range(unsigned int fd, unsigned int max_fd, unsigned int flags)
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

static inline int move_mount(int from_dfd, const char *from_pathname, int to_dfd,
			     const char *to_pathname, unsigned int flags)
{
	return syscall(__NR_move_mount, from_dfd, from_pathname, to_dfd,
		       to_pathname, flags);
}

/* arg1 of prctl() */
#ifndef PR_SCHED_CORE
#define PR_SCHED_CORE 62
#endif

/* arg2 of prctl() */
#ifndef PR_SCHED_CORE_GET
#define PR_SCHED_CORE_GET 0
#endif

#ifndef PR_SCHED_CORE_CREATE
#define PR_SCHED_CORE_CREATE 1 /* create unique core_sched cookie */
#endif

#ifndef PR_SCHED_CORE_SHARE_TO
#define PR_SCHED_CORE_SHARE_TO 2 /* push core_sched cookie to pid */
#endif

#ifndef PR_SCHED_CORE_SHARE_FROM
#define PR_SCHED_CORE_SHARE_FROM 3 /* pull core_sched cookie to pid */
#endif

#ifndef PR_SCHED_CORE_MAX
#define PR_SCHED_CORE_MAX 4
#endif

/* arg3 of prctl() */
#ifndef PR_SCHED_CORE_SCOPE_THREAD
#define PR_SCHED_CORE_SCOPE_THREAD 0
#endif

#ifndef PR_SCHED_CORE_SCOPE_THREAD_GROUP
#define PR_SCHED_CORE_SCOPE_THREAD_GROUP 1
#endif

#ifndef PR_SCHED_CORE_SCOPE_PROCESS_GROUP
#define PR_SCHED_CORE_SCOPE_PROCESS_GROUP 2
#endif

#define INVALID_SCHED_CORE_COOKIE ((__u64)-1)

static inline bool core_scheduling_cookie_valid(__u64 cookie)
{
	return (cookie > 0) && (cookie != INVALID_SCHED_CORE_COOKIE);
}

static inline __u64 core_scheduling_cookie_get(pid_t pid)
{
	__u64 cookie;
	int ret;

	ret = prctl(PR_SCHED_CORE, PR_SCHED_CORE_GET, pid,
		    PR_SCHED_CORE_SCOPE_THREAD, (unsigned long)&cookie);
	if (ret)
		return INVALID_SCHED_CORE_COOKIE;

	return cookie;
}

static inline int core_scheduling_cookie_create_threadgroup(pid_t pid)
{
	int ret;

	ret = prctl(PR_SCHED_CORE, PR_SCHED_CORE_CREATE, pid,
		    PR_SCHED_CORE_SCOPE_THREAD_GROUP, 0);
	if (ret)
		return -errno;

	return 0;
}

static inline int core_scheduling_cookie_create_thread(pid_t pid)
{
	int ret;

	ret = prctl(PR_SCHED_CORE, PR_SCHED_CORE_CREATE, pid,
		    PR_SCHED_CORE_SCOPE_THREAD, 0);
	if (ret)
		return -errno;

	return 0;
}

static inline int core_scheduling_cookie_share_with(pid_t pid)
{
	return prctl(PR_SCHED_CORE, PR_SCHED_CORE_SHARE_FROM, pid,
		     PR_SCHED_CORE_SCOPE_THREAD, 0);
}

static inline int core_scheduling_cookie_share_to(pid_t pid)
{
	return prctl(PR_SCHED_CORE, PR_SCHED_CORE_SHARE_TO, pid,
		     PR_SCHED_CORE_SCOPE_THREAD, 0);
}

static int kcmp(pid_t pid1, pid_t pid2, int type, unsigned long idx1,
		unsigned long idx2)
{
	return syscall(__NR_kcmp, pid1, pid2, type, idx1, idx2);
}

static inline bool filetable_shared(pid_t pid1, pid_t pid2)
{
	return kcmp(pid1, pid2, KCMP_FILES, -EBADF, -EBADF) == 0;
}

#endif /* __LXD_SYSCALL_WRAPPER_H */
