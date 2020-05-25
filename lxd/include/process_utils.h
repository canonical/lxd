/* SPDX-License-Identifier: LGPL-2.1+ */

#ifndef __LXD_PROCESS_UTILS_H
#define __LXD_PROCESS_UTILS_H

#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <linux/sched.h>
#include <sched.h>
#include <signal.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/syscall.h>
#include <unistd.h>

#include "compiler.h"
#include "syscall_numbers.h"

#ifndef CSIGNAL
#define CSIGNAL 0x000000ff /* signal mask to be sent at exit */
#endif

#ifndef CLONE_VM
#define CLONE_VM 0x00000100 /* set if VM shared between processes */
#endif

#ifndef CLONE_FS
#define CLONE_FS 0x00000200 /* set if fs info shared between processes */
#endif

#ifndef CLONE_FILES
#define CLONE_FILES 0x00000400 /* set if open files shared between processes */
#endif

#ifndef CLONE_SIGHAND
#define CLONE_SIGHAND 0x00000800 /* set if signal handlers and blocked signals shared */
#endif

#ifndef CLONE_PIDFD
#define CLONE_PIDFD 0x00001000 /* set if a pidfd should be placed in parent */
#endif

#ifndef CLONE_PTRACE
#define CLONE_PTRACE 0x00002000 /* set if we want to let tracing continue on the child too */
#endif

#ifndef CLONE_VFORK
#define CLONE_VFORK 0x00004000 /* set if the parent wants the child to wake it up on mm_release */
#endif

#ifndef CLONE_PARENT
#define CLONE_PARENT 0x00008000 /* set if we want to have the same parent as the cloner */
#endif

#ifndef CLONE_THREAD
#define CLONE_THREAD 0x00010000 /* Same thread group? */
#endif

#ifndef CLONE_NEWNS
#define CLONE_NEWNS 0x00020000 /* New mount namespace group */
#endif

#ifndef CLONE_SYSVSEM
#define CLONE_SYSVSEM 0x00040000 /* share system V SEM_UNDO semantics */
#endif

#ifndef CLONE_SETTLS
#define CLONE_SETTLS 0x00080000 /* create a new TLS for the child */
#endif

#ifndef CLONE_PARENT_SETTID
#define CLONE_PARENT_SETTID 0x00100000 /* set the TID in the parent */
#endif

#ifndef CLONE_CHILD_CLEARTID
#define CLONE_CHILD_CLEARTID 0x00200000 /* clear the TID in the child */
#endif

#ifndef CLONE_DETACHED
#define CLONE_DETACHED 0x00400000 /* Unused, ignored */
#endif

#ifndef CLONE_UNTRACED
#define CLONE_UNTRACED 0x00800000 /* set if the tracing process can't force CLONE_PTRACE on this clone */
#endif

#ifndef CLONE_CHILD_SETTID
#define CLONE_CHILD_SETTID 0x01000000 /* set the TID in the child */
#endif

#ifndef CLONE_NEWCGROUP
#define CLONE_NEWCGROUP 0x02000000 /* New cgroup namespace */
#endif

#ifndef CLONE_NEWUTS
#define CLONE_NEWUTS 0x04000000 /* New utsname namespace */
#endif

#ifndef CLONE_NEWIPC
#define CLONE_NEWIPC 0x08000000 /* New ipc namespace */
#endif

#ifndef CLONE_NEWUSER
#define CLONE_NEWUSER 0x10000000 /* New user namespace */
#endif

#ifndef CLONE_NEWPID
#define CLONE_NEWPID 0x20000000 /* New pid namespace */
#endif

#ifndef CLONE_NEWNET
#define CLONE_NEWNET 0x40000000 /* New network namespace */
#endif

#ifndef CLONE_IO
#define CLONE_IO 0x80000000 /* Clone io context */
#endif

/* Flags for the clone3() syscall. */
#ifndef CLONE_CLEAR_SIGHAND
#define CLONE_CLEAR_SIGHAND 0x100000000ULL /* Clear any signal handler and reset to SIG_DFL. */
#endif

#ifndef CLONE_INTO_CGROUP
#define CLONE_INTO_CGROUP 0x200000000ULL /* Clone into a specific cgroup given the right permissions. */
#endif

/*
 * cloning flags intersect with CSIGNAL so can be used with unshare and clone3
 * syscalls only:
 */
#ifndef CLONE_NEWTIME
#define CLONE_NEWTIME 0x00000080 /* New time namespace */
#endif

/* waitid */
#ifndef P_PIDFD
#define P_PIDFD 3
#endif

#ifndef CLONE_ARGS_SIZE_VER0
#define CLONE_ARGS_SIZE_VER0 64 /* sizeof first published struct */
#endif

#ifndef CLONE_ARGS_SIZE_VER1
#define CLONE_ARGS_SIZE_VER1 80 /* sizeof second published struct */
#endif

#ifndef CLONE_ARGS_SIZE_VER2
#define CLONE_ARGS_SIZE_VER2 88 /* sizeof third published struct */
#endif

#ifndef ptr_to_u64
#define ptr_to_u64(ptr) ((__u64)((uintptr_t)(ptr)))
#endif
#ifndef u64_to_ptr
#define u64_to_ptr(x) ((void *)(uintptr_t)x)
#endif

struct lxd_clone_args {
	__aligned_u64 flags;
	__aligned_u64 pidfd;
	__aligned_u64 child_tid;
	__aligned_u64 parent_tid;
	__aligned_u64 exit_signal;
	__aligned_u64 stack;
	__aligned_u64 stack_size;
	__aligned_u64 tls;
	__aligned_u64 set_tid;
	__aligned_u64 set_tid_size;
	__aligned_u64 cgroup;
};

__returns_twice static inline pid_t lxd_clone3(struct lxd_clone_args *args, size_t size)
{
	return syscall(__NR_clone3, args, size);
}

static inline int pidfd_open(pid_t pid, unsigned int flags)
{
	return syscall(__NR_pidfd_open, pid, flags);
}

static inline int pidfd_send_signal(int pidfd, int sig, siginfo_t *info,
				    unsigned int flags)
{
	return syscall(__NR_pidfd_send_signal, pidfd, sig, info, flags);
}

#endif /* __LXD_PROCESS_UTILS_H */

