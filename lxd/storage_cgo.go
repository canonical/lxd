// +build linux
// +build cgo

package main

/*
#define _GNU_SOURCE
#define _FILE_OFFSET_BITS 64
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <linux/loop.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#ifndef LO_FLAGS_AUTOCLEAR
#define LO_FLAGS_AUTOCLEAR 4
#endif

static int get_unused_loop_dev_legacy(char *loop_name)
{
	struct dirent *dp;
	struct loop_info64 lo64;
	DIR *dir;
	int dfd = -1, fd = -1, ret = -1;

	dir = opendir("/dev");
	if (!dir)
		return -1;

	while ((dp = readdir(dir))) {
		if (!dp)
			break;

		if (strncmp(dp->d_name, "loop", 4) != 0)
			continue;

		dfd = dirfd(dir);
		if (dfd < 0)
			continue;

		fd = openat(dfd, dp->d_name, O_RDWR);
		if (fd < 0)
			continue;

		ret = ioctl(fd, LOOP_GET_STATUS64, &lo64);
		if (ret < 0)

			if (ioctl(fd, LOOP_GET_STATUS64, &lo64) == 0 ||
			    errno != ENXIO) {
				close(fd);
				fd = -1;
				continue;
			}

		ret = snprintf(loop_name, LO_NAME_SIZE, "/dev/%s", dp->d_name);
		if (ret < 0 || ret >= LO_NAME_SIZE) {
			close(fd);
			fd = -1;
			continue;
		}

		break;
	}

	closedir(dir);

	if (fd < 0)
		return -1;

	return fd;
}

static int get_unused_loop_dev(char *name_loop)
{
	int loop_nr, ret;
	int fd_ctl = -1, fd_tmp = -1;

	fd_ctl = open("/dev/loop-control", O_RDWR | O_CLOEXEC);
	if (fd_ctl < 0)
		return -ENODEV;

	loop_nr = ioctl(fd_ctl, LOOP_CTL_GET_FREE);
	if (loop_nr < 0)
		goto on_error;

	ret = snprintf(name_loop, LO_NAME_SIZE, "/dev/loop%d", loop_nr);
	if (ret < 0 || ret >= LO_NAME_SIZE)
		goto on_error;

	fd_tmp = open(name_loop, O_RDWR | O_CLOEXEC);
	if (fd_tmp < 0)
		goto on_error;

on_error:
	close(fd_ctl);
	return fd_tmp;
}

int prepare_loop_dev(const char *source, char *loop_dev)
{
	int ret;
	struct loop_info64 lo64;
	int fd_img = -1, fret = -1, fd_loop = -1;

	fd_loop = get_unused_loop_dev(loop_dev);
	if (fd_loop < 0) {
		if (fd_loop == -ENODEV)
			fd_loop = get_unused_loop_dev_legacy(loop_dev);
		else
			goto on_error;
	}

	fd_img = open(source, O_RDWR | O_CLOEXEC);
	if (fd_img < 0)
		goto on_error;

	ret = ioctl(fd_loop, LOOP_SET_FD, fd_img);
	if (ret < 0)
		goto on_error;

	memset(&lo64, 0, sizeof(lo64));
	lo64.lo_flags = LO_FLAGS_AUTOCLEAR;

	ret = ioctl(fd_loop, LOOP_SET_STATUS64, &lo64);
	if (ret < 0)
		goto on_error;

	fret = 0;

on_error:
	if (fd_img >= 0)
		close(fd_img);

	if (fret < 0 && fd_loop >= 0) {
		close(fd_loop);
		fd_loop = -1;
	}

	return fd_loop;
}
*/
import "C"

import (
	"fmt"
	"os"
	"unsafe"
)

// prepareLoopDev() detects and sets up a loop device for source. It returns an
// open file descriptor to the free loop device and the path of the free loop
// device. It's the callers responsibility to close the open file descriptor.
func prepareLoopDev(source string) (*os.File, error) {
	cLoopDev := C.malloc(C.size_t(C.LO_NAME_SIZE))
	if cLoopDev == nil {
		return nil, fmt.Errorf("Failed to allocate memory in C.")
	}
	defer C.free(cLoopDev)

	cSource := C.CString(source)
	defer C.free(unsafe.Pointer(cSource))
	loopFd := int(C.prepare_loop_dev(cSource, (*C.char)(cLoopDev)))
	if loopFd < 0 {
		return nil, fmt.Errorf("Failed to prepare loop device.")
	}

	return os.NewFile(uintptr(loopFd), C.GoString((*C.char)(cLoopDev))), nil
}
