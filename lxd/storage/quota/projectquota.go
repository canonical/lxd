package quota

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"github.com/lxc/lxd/shared"
)

/*
#include <linux/fs.h>
#include <linux/dqblk_xfs.h>
#include <sys/ioctl.h>
#include <sys/quota.h>
#include <sys/types.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdlib.h>

int quota_supported(char *dev_path) {
	struct if_dqinfo dqinfo;

	return quotactl(QCMD(Q_GETINFO, PRJQUOTA), dev_path, 0, (caddr_t)&dqinfo);
}

int quota_get_usage(char *dev_path, uint32_t id) {
	struct if_dqblk quota;

	if (quotactl(QCMD(Q_GETQUOTA, PRJQUOTA), dev_path, id, (caddr_t)&quota) < 0) {
		return -1;
	}

	return quota.dqb_curspace;
}


int quota_set(char *dev_path, uint32_t id, int hard_bytes) {
	struct if_dqblk quota;
	fs_disk_quota_t xfsquota;

	if (quotactl(QCMD(Q_GETQUOTA, PRJQUOTA), dev_path, id, (caddr_t)&quota) < 0) {
		return -1;
	}

	quota.dqb_bhardlimit = hard_bytes;
	if (quotactl(QCMD(Q_SETQUOTA, PRJQUOTA), dev_path, id, (caddr_t)&quota) < 0) {
		xfsquota.d_version = FS_DQUOT_VERSION;
		xfsquota.d_id = id;
		xfsquota.d_flags = FS_PROJ_QUOTA;
		xfsquota.d_fieldmask = FS_DQ_BHARD;
		xfsquota.d_blk_hardlimit = hard_bytes * 1024 / 512;

		if (quotactl(QCMD(Q_XSETQLIM, PRJQUOTA), dev_path, id, (caddr_t)&xfsquota) < 0) {
			return -1;
		}
	}

	return 0;
}

int quota_set_path(char *path, uint32_t id) {
	struct fsxattr attr;
	int fd;
	int ret;

	fd = open(path, O_RDONLY | O_CLOEXEC);
	if (fd < 0)
		return -1;

	ret = ioctl(fd, FS_IOC_FSGETXATTR, &attr);
	if (ret < 0) {
		return -1;
	}

	attr.fsx_xflags |= FS_XFLAG_PROJINHERIT;
	attr.fsx_projid = id;

	ret = ioctl(fd, FS_IOC_FSSETXATTR, &attr);
	if (ret < 0) {
		return -1;
	}

	return 0;
}

int32_t quota_get_path(char *path) {
	struct fsxattr attr;
	int fd;
	int ret;

	fd = open(path, O_RDONLY | O_CLOEXEC);
	if (fd < 0)
		return -1;

	ret = ioctl(fd, FS_IOC_FSGETXATTR, &attr);
	if (ret < 0) {
		return -1;
	}

	return attr.fsx_projid;
}

*/
import "C"

var errNoDevice = fmt.Errorf("Couldn't find backing device for mountpoint")

func devForPath(path string) (string, error) {
	// Get major/minor
	var stat syscall.Stat_t
	err := syscall.Lstat(path, &stat)
	if err != nil {
		return "", err
	}

	devMajor := shared.Major(stat.Dev)
	devMinor := shared.Minor(stat.Dev)

	// Parse mountinfo for it
	mountinfo, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return "", err
	}
	defer mountinfo.Close()

	scanner := bufio.NewScanner(mountinfo)
	for scanner.Scan() {
		line := scanner.Text()

		tokens := strings.Fields(line)
		if len(tokens) < 5 {
			continue
		}

		if tokens[2] == fmt.Sprintf("%d:%d", devMajor, devMinor) {
			if shared.PathExists(tokens[len(tokens)-2]) {
				return tokens[len(tokens)-2], nil
			}
		}
	}

	return "", errNoDevice
}

// Supported check if the given path supports project quotas
func Supported(path string) (bool, error) {
	// Get the backing device
	devPath, err := devForPath(path)
	if err != nil {
		return false, err
	}

	// Call quotactl through CGo
	cDevPath := C.CString(devPath)
	defer C.free(unsafe.Pointer(cDevPath))

	return C.quota_supported(cDevPath) == 0, nil
}

// GetProject returns the project quota ID for the given path
func GetProject(path string) (uint32, error) {
	// Call ioctl through CGo
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	id := C.quota_get_path(cPath)
	if id < 0 {
		return 0, fmt.Errorf("Failed to get project from '%s'", path)
	}

	return uint32(id), nil
}

// SetProject sets the project quota ID for the given path
func SetProject(path string, id uint32) error {
	// Call ioctl through CGo
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	if C.quota_set_path(cPath, C.uint32_t(id)) != 0 {
		return fmt.Errorf("Failed to set project id '%d' on '%s'", id, path)
	}

	return nil
}

// DeleteProject unsets the project id from the path and clears the quota for the project id
func DeleteProject(path string, id uint32) error {
	// Unset the project from the path
	err := SetProject(path, 0)
	if err != nil {
		return err
	}

	// Unset the quota on the project
	err = SetProjectQuota(path, id, 0)
	if err != nil {
		return err
	}

	return nil
}

// GetProjectUsage returns the current consumption
func GetProjectUsage(path string, id uint32) (int64, error) {
	// Get the backing device
	devPath, err := devForPath(path)
	if err != nil {
		return -1, err
	}

	// Call quotactl through CGo
	cDevPath := C.CString(devPath)
	defer C.free(unsafe.Pointer(cDevPath))

	size := C.quota_get_usage(cDevPath, C.uint32_t(id))
	if size < 0 {
		return -1, fmt.Errorf("Failed to get project consumption for id '%d' on '%s'", id, path)
	}

	return int64(size), nil
}

// SetProjectQuota sets the quota on the project id
func SetProjectQuota(path string, id uint32, bytes int64) error {
	// Get the backing device
	devPath, err := devForPath(path)
	if err != nil {
		return err
	}

	// Call quotactl through CGo
	cDevPath := C.CString(devPath)
	defer C.free(unsafe.Pointer(cDevPath))

	if C.quota_set(cDevPath, C.uint32_t(id), C.int(bytes/1024)) != 0 {
		return fmt.Errorf("Failed to set project quota for id '%d' on '%s'", id, path)
	}

	return nil
}
