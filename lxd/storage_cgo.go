// +build linux
// +build cgo

package main

/*
#define _GNU_SOURCE
#define _FILE_OFFSET_BITS 64
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <linux/loop.h>
#include <sys/ioctl.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/types.h>

#ifndef LO_FLAGS_AUTOCLEAR
#define LO_FLAGS_AUTOCLEAR 4
#endif

#ifndef MS_LAZYTIME
#define MS_LAZYTIME (1<<25)
#endif

#define LXD_MAXPATH 4096
#define LXD_NUMSTRLEN64 21
#define LXD_MAX_LOOP_PATHLEN (2 * sizeof("loop/")) + LXD_NUMSTRLEN64 + sizeof("backing_file") + 1

// If a loop file is already associated with a loop device, find it.
// This looks at "/sys/block" to avoid having to parse all of "/dev". Also, this
// allows to retrieve the full name of the backing file even if
// strlen(backing file) > LO_NAME_SIZE.
static int find_associated_loop_device(const char *loop_file,
				       char *loop_dev_name)
{
	char looppath[LXD_MAX_LOOP_PATHLEN];
	char buf[LXD_MAXPATH];
	struct dirent *dp;
	DIR *dir;
	int dfd = -1, fd = -1;

	dir = opendir("/sys/block");
	if (!dir)
		return -1;

	while ((dp = readdir(dir))) {
		int ret;
		size_t totlen;
		struct stat fstatbuf;

		if (!dp)
			break;

		if (strncmp(dp->d_name, "loop", 4))
			continue;

		dfd = dirfd(dir);
		if (dfd < 0)
			continue;

		ret = snprintf(looppath, sizeof(looppath), "%s/loop/backing_file", dp->d_name);
		if (ret < 0 || (size_t)ret >= sizeof(looppath))
			continue;

		ret = fstatat(dfd, looppath, &fstatbuf, 0);
		if (ret < 0)
			continue;

		fd = openat(dfd, looppath, O_RDONLY | O_CLOEXEC, 0);
		if (ret < 0)
			continue;

		// Clear buffer.
		memset(buf, 0, sizeof(buf));
		ret = read(fd, buf, sizeof(buf));
		if (ret < 0)
			continue;
		close(fd);
		fd = -1;

		totlen = strlen(buf);

		// Trim newlines.
		while ((totlen > 0) && (buf[totlen - 1] == '\n'))
			buf[--totlen] = '\0';

		if (strcmp(buf, loop_file))
			continue;

		// Create path to loop device.
		ret = snprintf(loop_dev_name, LO_NAME_SIZE, "/dev/%s",
			       dp->d_name);
		if (ret < 0 || ret >= LO_NAME_SIZE)
			continue;

		// Open fd to loop device.
		fd = open(loop_dev_name, O_RDWR);
		break;
	}

	closedir(dir);

	if (fd < 0)
		return -1;

	return fd;
}

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
		if (ret < 0) {
			if (ioctl(fd, LOOP_GET_STATUS64, &lo64) == 0 ||
			    errno != ENXIO) {
				close(fd);
				fd = -1;
				continue;
			}
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

static int prepare_loop_dev(const char *source, char *loop_dev, int flags)
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
	lo64.lo_flags = flags;

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

static inline int prepare_loop_dev_retry(const char *source, char *loop_dev, int flags)
{
	int ret;
	unsigned int idx = 0;

	do {
		ret = prepare_loop_dev(source, loop_dev, flags);
		idx++;
	} while (ret < 0 && errno == EBUSY && idx < 30);

	return ret;
}

// Note that this does not guarantee to clear the loop device in time so that
// find_associated_loop_device() will not report that there still is a
// configured device (udev and so on...). So don't call
// find_associated_loop_device() after having called
// set_autoclear_loop_device().
int set_autoclear_loop_device(int fd_loop)
{
	struct loop_info64 lo64;

	memset(&lo64, 0, sizeof(lo64));
	lo64.lo_flags = LO_FLAGS_AUTOCLEAR;
	errno = 0;
	return ioctl(fd_loop, LOOP_SET_STATUS64, &lo64);
}

// Unset the LO_FLAGS_AUTOCLEAR flag on the given loop device file descriptor.
int unset_autoclear_loop_device(int fd_loop)
{
	int ret;
	struct loop_info64 lo64;

	errno = 0;
	ret = ioctl(fd_loop, LOOP_GET_STATUS64, &lo64);
	if (ret < 0)
		return -1;

	if ((lo64.lo_flags & LO_FLAGS_AUTOCLEAR) == 0)
		return 0;

	lo64.lo_flags &= ~LO_FLAGS_AUTOCLEAR;
	errno = 0;
	return ioctl(fd_loop, LOOP_SET_STATUS64, &lo64);
}
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
import "C"

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"unsafe"

	"github.com/pkg/errors"
)

// LoFlagsAutoclear determines whether the loop device will autodestruct on last
// close.
const LoFlagsAutoclear int = C.LO_FLAGS_AUTOCLEAR

// MS_LAZYTIME retains inode timestamps in memory and updated them on-disk only
// under certain conditions.
const MS_LAZYTIME uintptr = C.MS_LAZYTIME

// prepareLoopDev() detects and sets up a loop device for source. It returns an
// open file descriptor to the free loop device and the path of the free loop
// device. It's the callers responsibility to close the open file descriptor.
func prepareLoopDev(source string, flags int) (*os.File, error) {
	cLoopDev := C.malloc(C.size_t(C.LO_NAME_SIZE))
	if cLoopDev == nil {
		return nil, fmt.Errorf("Failed to allocate memory in C")
	}
	defer C.free(cLoopDev)

	cSource := C.CString(source)
	defer C.free(unsafe.Pointer(cSource))
	loopFd, _ := C.find_associated_loop_device(cSource, (*C.char)(cLoopDev))
	if loopFd >= 0 {
		return os.NewFile(uintptr(loopFd), C.GoString((*C.char)(cLoopDev))), nil
	}

	loopFd, err := C.prepare_loop_dev_retry(cSource, (*C.char)(cLoopDev), C.int(flags))
	if loopFd < 0 {
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to prepare loop device for %q", source)
		}

		return nil, fmt.Errorf("Failed to prepare loop device for %q", source)
	}

	return os.NewFile(uintptr(loopFd), C.GoString((*C.char)(cLoopDev))), nil
}

func setAutoclearOnLoopDev(loopFd int) error {
	ret, err := C.set_autoclear_loop_device(C.int(loopFd))
	if ret < 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("Failed to set LO_FLAGS_AUTOCLEAR")
	}

	return nil
}

func unsetAutoclearOnLoopDev(loopFd int) error {
	ret, err := C.unset_autoclear_loop_device(C.int(loopFd))
	if ret < 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("Failed to unset LO_FLAGS_AUTOCLEAR")
	}

	return nil
}

func loopDeviceHasBackingFile(loopDevice string, loopFile string) (*os.File, error) {
	lidx := strings.LastIndex(loopDevice, "/")
	if lidx < 0 {
		return nil, fmt.Errorf("Invalid loop device path: \"%s\"", loopDevice)
	}

	loopName := loopDevice[(lidx + 1):]
	backingFile := fmt.Sprintf("/sys/block/%s/loop/backing_file", loopName)
	contents, err := ioutil.ReadFile(backingFile)
	if err != nil {
		return nil, err
	}

	cleanBackingFile := strings.TrimSpace(string(contents))
	if cleanBackingFile != loopFile {
		return nil, fmt.Errorf("loop device has new backing file: \"%s\"", cleanBackingFile)
	}

	return os.OpenFile(loopDevice, os.O_RDWR, 0660)
}
