// +build linux
// +build cgo

package idmap

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"github.com/lxc/lxd/shared"
)

// #cgo LDFLAGS: -lacl
/*
#define _GNU_SOURCE
#include <byteswap.h>
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/capability.h>
#include <unistd.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/acl.h>

// Needs to be included at the end
#include <sys/xattr.h>

#ifndef VFS_CAP_REVISION_1
#define VFS_CAP_REVISION_1 0x01000000
#endif

#ifndef VFS_CAP_REVISION_2
#define VFS_CAP_REVISION_2 0x02000000
#endif

#ifndef VFS_CAP_REVISION_3
#define VFS_CAP_REVISION_3 0x03000000
struct vfs_ns_cap_data {
	__le32 magic_etc;
	struct {
		__le32 permitted;
		__le32 inheritable;
	} data[VFS_CAP_U32];
	__le32 rootid;
};
#endif

#if __BYTE_ORDER == __BIG_ENDIAN
#define BE32_TO_LE32(x) bswap_32(x)
#else
#define BE32_TO_LE32(x) (x)
#endif

int set_vfs_ns_caps(char *path, char *caps, ssize_t len, uint32_t uid)
{
	// Works because vfs_ns_cap_data is a superset of vfs_cap_data (rootid
	// field added to the end)
	struct vfs_ns_cap_data ns_xattr;

	memset(&ns_xattr, 0, sizeof(ns_xattr));
	memcpy(&ns_xattr, caps, len);
	ns_xattr.magic_etc &= ~(VFS_CAP_REVISION_1 | VFS_CAP_REVISION_2);
	ns_xattr.magic_etc |= VFS_CAP_REVISION_3;
	ns_xattr.rootid = BE32_TO_LE32(uid);

	return setxattr(path, "security.capability", &ns_xattr, sizeof(ns_xattr), 0);
}

int set_dummy_fs_ns_caps(const char *path)
{
	#define __raise_cap_permitted(x, ns_cap_data)   ns_cap_data.data[(x)>>5].permitted   |= (1<<((x)&31))

	struct vfs_ns_cap_data ns_xattr;

	memset(&ns_xattr, 0, sizeof(ns_xattr));
        __raise_cap_permitted(CAP_NET_RAW, ns_xattr);
	ns_xattr.magic_etc |= VFS_CAP_REVISION_3 | VFS_CAP_FLAGS_EFFECTIVE;
	ns_xattr.rootid = BE32_TO_LE32(1000000);

	return setxattr(path, "security.capability", &ns_xattr, sizeof(ns_xattr), 0);
}

int shiftowner(char *basepath, char *path, int uid, int gid)
{
	int fd, ret;
	char fdpath[PATH_MAX], realpath[PATH_MAX];
	struct stat sb;

	fd = open(path, O_PATH | O_NOFOLLOW);
	if (fd < 0) {
		perror("Failed open");
		return 1;
	}

	ret = sprintf(fdpath, "/proc/self/fd/%d", fd);
	if (ret < 0) {
		perror("Failed sprintf");
		close(fd);
		return 1;
	}

	ret = readlink(fdpath, realpath, PATH_MAX);
	if (ret < 0) {
		perror("Failed readlink");
		close(fd);
		return 1;
	}

	if (strlen(realpath) < strlen(basepath)) {
		printf("Invalid path, source (%s) is outside of basepath (%s)\n", realpath, basepath);
		close(fd);
		return 1;
	}

	if (strncmp(realpath, basepath, strlen(basepath))) {
		printf("Invalid path, source (%s) is outside of basepath " "(%s).\n", realpath, basepath);
		close(fd);
		return 1;
	}

	ret = fstat(fd, &sb);
	if (ret < 0) {
		perror("Failed fstat");
		close(fd);
		return 1;
	}

	ret = fchownat(fd, "", uid, gid, AT_EMPTY_PATH | AT_SYMLINK_NOFOLLOW);
	if (ret < 0) {
		perror("Failed chown");
		close(fd);
		return 1;
	}

	if (!S_ISLNK(sb.st_mode)) {
		ret = chmod(fdpath, sb.st_mode);
		if (ret < 0) {
			perror("Failed chmod");
			close(fd);
			return 1;
		}
	}

	close(fd);
	return 0;
}
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
import "C"

// ShiftOwner updates uid and gid for a file when entering/exiting a namespace
func ShiftOwner(basepath string, path string, uid int, gid int) error {
	cbasepath := C.CString(basepath)
	defer C.free(unsafe.Pointer(cbasepath))

	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	r := C.shiftowner(cbasepath, cpath, C.int(uid), C.int(gid))
	if r != 0 {
		return fmt.Errorf("Failed to change ownership of: %s", path)
	}

	return nil
}

// GetCaps extracts the list of capabilities effective on the file
func GetCaps(path string) ([]byte, error) {
	xattrs, err := shared.GetAllXattr(path)
	if err != nil {
		return nil, err
	}

	valueStr, ok := xattrs["security.capability"]
	if !ok {
		return nil, nil
	}

	return []byte(valueStr), nil
}

// SetCaps applies the caps for a particular root uid
func SetCaps(path string, caps []byte, uid int64) error {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	ccaps := C.CString(string(caps))
	defer C.free(unsafe.Pointer(ccaps))

	r := C.set_vfs_ns_caps(cpath, ccaps, C.ssize_t(len(caps)), C.uint32_t(uid))
	if r != 0 {
		return fmt.Errorf("Failed to apply capabilities to: %s", path)
	}

	return nil
}

// ShiftACL updates uid and gid for file ACLs when entering/exiting a namespace
func ShiftACL(path string, shiftIds func(uid int64, gid int64) (int64, int64)) error {
	err := shiftAclType(path, C.ACL_TYPE_ACCESS, shiftIds)
	if err != nil {
		return err
	}

	err = shiftAclType(path, C.ACL_TYPE_DEFAULT, shiftIds)
	if err != nil {
		return err
	}

	return nil
}

func shiftAclType(path string, aclType int, shiftIds func(uid int64, gid int64) (int64, int64)) error {
	// Convert the path to something usable with cgo
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	// Read the current ACL set for the requested type
	acl := C.acl_get_file(cpath, C.uint(aclType))
	if acl == nil {
		return nil
	}
	defer C.acl_free(unsafe.Pointer(acl))

	newAcl := C.acl_init(0)
	defer C.acl_free(unsafe.Pointer(newAcl))

	// Iterate through all ACL entries
	update := false
	for entryId := C.ACL_FIRST_ENTRY; ; entryId = C.ACL_NEXT_ENTRY {
		var ent C.acl_entry_t
		var newEnt C.acl_entry_t
		var tag C.acl_tag_t

		// Get the ACL entry
		ret := C.acl_get_entry(acl, C.int(entryId), &ent)
		if ret == 0 {
			break
		} else if ret < 0 {
			return fmt.Errorf("Failed to get the ACL entry for %s", path)
		}

		// Setup the new entry
		ret = C.acl_create_entry(&newAcl, &newEnt)
		if ret == -1 {
			return fmt.Errorf("Failed to allocate a new ACL entry for %s", path)
		}

		ret = C.acl_copy_entry(newEnt, ent)
		if ret == -1 {
			return fmt.Errorf("Failed to copy the ACL entry for %s", path)
		}

		// Get the ACL type
		ret = C.acl_get_tag_type(newEnt, &tag)
		if ret == -1 {
			return fmt.Errorf("Failed to get the ACL type for %s", path)
		}

		// We only care about user and group ACLs, copy anything else
		if tag != C.ACL_USER && tag != C.ACL_GROUP {
			continue
		}

		// Get the value
		idp := (*C.id_t)(C.acl_get_qualifier(newEnt))
		if idp == nil {
			return fmt.Errorf("Failed to get current ACL value for %s", path)
		}

		// Shift the value
		newId, _ := shiftIds((int64)(*idp), -1)

		// Update the new entry with the shifted value
		ret = C.acl_set_qualifier(newEnt, unsafe.Pointer(&newId))
		if ret == -1 {
			return fmt.Errorf("Failed to set ACL qualifier on %s", path)
		}

		update = true
	}

	// Update the on-disk ACLs to match
	if update {
		ret := C.acl_set_file(cpath, C.uint(aclType), newAcl)
		if ret == -1 {
			return fmt.Errorf("Failed to change ACLs on %s", path)
		}
	}

	return nil
}

func SupportsVFS3Fscaps(prefix string) bool {
	tmpfile, err := ioutil.TempFile(prefix, ".lxd_fcaps_v3_")
	if err != nil {
		return false
	}
	tmpfile.Close()
	defer os.Remove(tmpfile.Name())

	err = os.Chmod(tmpfile.Name(), 0001)
	if err != nil {
		return false
	}

	cpath := C.CString(tmpfile.Name())
	defer C.free(unsafe.Pointer(cpath))

	r := C.set_dummy_fs_ns_caps(cpath)
	if r != 0 {
		return false
	}

	cmd := exec.Command(tmpfile.Name())
	err = cmd.Run()
	if err != nil {
		errno, isErrno := shared.GetErrno(err)
		if isErrno && (errno == syscall.ERANGE || errno == syscall.EOVERFLOW) {
			return false
		}

		return true
	}

	return true
}
