/* SPDX-License-Identifier: LGPL-2.1+ */
#ifndef __LXD_SYSCALL_NUMBERS_H
#define __LXD_SYSCALL_NUMBERS_H

#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <asm/unistd.h>
#include <errno.h>
#include <linux/keyctl.h>
#include <sched.h>
#include <stdint.h>
#include <sys/syscall.h>
#include <sys/types.h>
#include <unistd.h>

#ifndef __NR_pidfd_open
	#if defined __alpha__
		#define __NR_pidfd_open 544
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_pidfd_open 4434
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_pidfd_open 6434
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_pidfd_open 5434
		#endif
	#elif defined __ia64__
		#define __NR_pidfd_open (434 + 1024)
	#else
		#define __NR_pidfd_open 434
	#endif
#endif

#ifndef __NR_pidfd_getfd
	#if defined __alpha__
		#define __NR_pidfd_getfd 548
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_pidfd_getfd 4438
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_pidfd_getfd 6438
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_pidfd_getfd 5438
		#endif
	#elif defined __ia64__
		#define __NR_pidfd_getfd (438 + 1024)
	#else
		#define __NR_pidfd_getfd 438
	#endif
#endif

#ifndef __NR_pidfd_send_signal
	#if defined __alpha__
		#define __NR_pidfd_send_signal 534
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_pidfd_send_signal 4424
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_pidfd_send_signal 6424
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_pidfd_send_signal 5424
		#endif
	#elif defined __ia64__
		#define __NR_pidfd_send_signal (424 + 1024)
	#else
		#define __NR_pidfd_send_signal 424
	#endif
#endif

#ifndef __NR_clone3
	#if defined __alpha__
		#define __NR_clone3 545
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_clone3 4435
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_clone3 6435
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_clone3 5435
		#endif
	#elif defined __ia64__
		#define __NR_clone3 (435 + 1024)
	#else
		#define __NR_clone3 435
	#endif
#endif

#endif /* __LXD_SYSCALL_NUMBERS_H */
