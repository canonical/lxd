#ifndef LXD_SECCOMP_H
#define LXD_SECCOMP_H

#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <linux/seccomp.h>
#include <linux/filter.h>
#include <linux/types.h>

#ifdef SECCOMP_GET_ACTION_AVAIL
#define SECCOMP_GET_ACTION_AVAIL 2
#endif

#ifndef SECCOMP_RET_ALLOW
#define SECCOMP_RET_ALLOW 0x7fff0000U
#endif

#ifndef SECCOMP_GET_NOTIF_SIZES
#define SECCOMP_GET_NOTIF_SIZES 3
#endif

#ifndef SECCOMP_USER_NOTIF_FLAG_CONTINUE
#define SECCOMP_USER_NOTIF_FLAG_CONTINUE 0x00000001
#endif

#ifndef SECCOMP_FILTER_FLAG_NEW_LISTENER
#define SECCOMP_FILTER_FLAG_NEW_LISTENER (1UL << 3)
#endif

#ifndef SECCOMP_RET_USER_NOTIF
#define SECCOMP_RET_USER_NOTIF 0x7fc00000U

struct seccomp_notif {
	__u64 id;
	__u32 pid;
	__u32 flags;
	struct seccomp_data data;
};

struct seccomp_notif_resp {
	__u64 id;
	__s64 val;
	__s32 error;
	__u32 flags;
};

struct seccomp_notif_sizes {
	__u16 seccomp_notif;
	__u16 seccomp_notif_resp;
	__u16 seccomp_data;
};

#define SECCOMP_IOC_MAGIC		'!'
#define SECCOMP_IO(nr)			_IO(SECCOMP_IOC_MAGIC, nr)
#define SECCOMP_IOR(nr, type)		_IOR(SECCOMP_IOC_MAGIC, nr, type)
#define SECCOMP_IOW(nr, type)		_IOW(SECCOMP_IOC_MAGIC, nr, type)
#define SECCOMP_IOWR(nr, type)		_IOWR(SECCOMP_IOC_MAGIC, nr, type)

#define SECCOMP_IOCTL_NOTIF_RECV	SECCOMP_IOWR(0, struct seccomp_notif)
#define SECCOMP_IOCTL_NOTIF_SEND	SECCOMP_IOWR(1,	\
						struct seccomp_notif_resp)
#define SECCOMP_IOCTL_NOTIF_ID_VALID	SECCOMP_IOR(2, __u64)
#endif
#endif /* LXD_SECCOMP_H */
