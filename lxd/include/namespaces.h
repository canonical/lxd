#ifndef __NAMESPACES_H

// Define setns() if missing from the C library 
#ifndef HAVE_SETNS
static inline int setns(int fd, int nstype) {
#ifdef __NR_setns
	return syscall(__NR_setns, fd, nstype);
#elif defined(__NR_set_ns)
	return syscall(__NR_set_ns, fd, nstype);
#else
	errno = ENOSYS;
	return -1; 
#endif
}
#endif

#endif
