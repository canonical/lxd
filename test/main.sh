#!/bin/sh

(
    cat << EOF
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <pthread.h>
#include <stddef.h>
#include <sched.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mount.h>
#include <sys/prctl.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <syscall.h>
#include <unistd.h>

#define ARRAY_SIZE(x) (sizeof(x) / sizeof(*(x)))
int main(int argc, char *argv[])
{
    int ret;

    struct sock_filter filter[] = {
        BPF_STMT(BPF_LD|BPF_W|BPF_ABS, offsetof(struct seccomp_data, nr)),
        //BPF_JUMP(BPF_JMP|BPF_JEQ|BPF_K, __NR_seccomp, 1, 0),
        BPF_JUMP(BPF_JMP|BPF_JEQ|BPF_K, __NR_io_uring_setup, 0, 1),
        BPF_STMT(BPF_RET|BPF_K, SECCOMP_RET_ERRNO | ENOSYS),
        BPF_STMT(BPF_RET|BPF_K, SECCOMP_RET_ALLOW),
    };
    struct sock_fprog prog = {
        .len = (unsigned short)ARRAY_SIZE(filter),
        .filter = filter,
    };

    ret = syscall(__NR_seccomp, SECCOMP_SET_MODE_FILTER, 0, &prog);
    if (ret)
        exit(EXIT_FAILURE);

    execlp("./main_orig.sh", "main_orig.sh", (argc >= 2) ? argv[1] : (char *)NULL, (char *)NULL);

    exit(EXIT_SUCCESS);
}
EOF
) > /tmp/main-lxd.$$.c

gcc -o /tmp/main-lxd.$$ /tmp/main-lxd.$$.c
chmod +x /tmp/main-lxd.$$

exec /tmp/main-lxd.$$ $@
