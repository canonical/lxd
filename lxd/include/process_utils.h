#ifndef __LXD_PROCESS_UTILS_H
#define __LXD_PROCESS_UTILS_H

#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <linux/sched.h>
#include <linux/types.h>
#include <sched.h>
#include <signal.h>
#include <stdbool.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/syscall.h>
#include <sys/ioctl.h>
#include <sys/wait.h>
#include <unistd.h>

#include "compiler.h"
#include "memory_utils.h"
#include "syscall_numbers.h"

static inline int lxd_pidfd_open(pid_t pid, unsigned int flags)
{
	return syscall(__NR_pidfd_open, pid, flags);
}

static inline int lxd_pidfd_send_signal(int pidfd, int sig, siginfo_t *info,
					unsigned int flags)
{
	return syscall(__NR_pidfd_send_signal, pidfd, sig, info, flags);
}

struct lxd_pidfd_info {
	__u64 mask;
	__u64 cgroupid;
	__u32 pid;
	__u32 tgid;
	__u32 ppid;
	__u32 ruid;
	__u32 rgid;
	__u32 euid;
	__u32 egid;
	__u32 suid;
	__u32 sgid;
	__u32 fsuid;
	__u32 fsgid;
	__s32 exit_code;
	__u32 coredump_mask;
	__u32 coredump_signal;
	__u32 coredump_code;
	__u32 coredump_pad;
	__u64 supported_mask;
};

#ifndef PIDFD_INFO_EXIT
#define PIDFD_INFO_EXIT (1UL << 3)
#endif

#ifndef PIDFD_GET_INFO
#define PIDFS_IOCTL_MAGIC 0xFF
#define PIDFD_GET_INFO _IOWR(PIDFS_IOCTL_MAGIC, 11, struct lxd_pidfd_info)
#endif

static inline int lxd_pidfd_get_exit_info(int pidfd, int *exit_code,
					  int *has_exit_code)
{
	struct lxd_pidfd_info info = {0};
	int ret;

	if (!exit_code || !has_exit_code)
		return ret_errno(EINVAL);

	info.mask = PIDFD_INFO_EXIT;

	ret = ioctl(pidfd, PIDFD_GET_INFO, &info);
	if (ret < 0)
		return -1;

	*exit_code = info.exit_code;
	*has_exit_code = !!(info.mask & PIDFD_INFO_EXIT);

	return 0;
}

static inline bool process_still_alive(int pidfd)
{
	return lxd_pidfd_send_signal(pidfd, 0, NULL, 0) == 0;
}

static inline int wait_for_pid(pid_t pid)
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

static inline int wait_for_pid_status_nointr(pid_t pid)
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

	return status;
}

static inline int append_null_to_list(void ***list)
{
	int newentry = 0;
	void **new_list;

	if (*list)
		for (; (*list)[newentry]; newentry++)
			;

	new_list = realloc(*list, (newentry + 2) * sizeof(void **));
	if (!new_list)
		return ret_errno(ENOMEM);

	*list = new_list;
	(*list)[newentry + 1] = NULL;
	return newentry;
}

static inline int push_vargs(char ***list, char *entry)
{
	__do_free char *copy = NULL;
	int newentry;

	copy = strdup(entry);
	if (!copy)
		return ret_errno(ENOMEM);

	newentry = append_null_to_list((void ***)list);
	if (newentry < 0)
		return newentry;

	(*list)[newentry] = move_ptr(copy);

	return 0;
}

#endif /* __LXD_PROCESS_UTILS_H */
