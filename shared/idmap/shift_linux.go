// +build linux
// +build cgo

package idmap

import (
	"fmt"
	"os"
	"unsafe"
)

// #cgo LDFLAGS: -lacl
/*
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/acl.h>

int shiftowner(char *basepath, char *path, int uid, int gid) {
	struct stat sb;
	int fd, r;
	char fdpath[PATH_MAX];
	char realpath[PATH_MAX];

	fd = open(path, O_PATH|O_NOFOLLOW);
	if (fd < 0 ) {
		perror("Failed open");
		return 1;
	}

	r = sprintf(fdpath, "/proc/self/fd/%d", fd);
	if (r < 0) {
		perror("Failed sprintf");
		close(fd);
		return 1;
	}

	r = readlink(fdpath, realpath, PATH_MAX);
	if (r < 0) {
		perror("Failed readlink");
		close(fd);
		return 1;
	}

	if (strlen(realpath) < strlen(basepath)) {
		printf("Invalid path, source (%s) is outside of basepath (%s).\n", realpath, basepath);
		close(fd);
		return 1;
	}

	if (strncmp(realpath, basepath, strlen(basepath))) {
		printf("Invalid path, source (%s) is outside of basepath (%s).\n", realpath, basepath);
		close(fd);
		return 1;
	}

	r = fstat(fd, &sb);
	if (r < 0) {
		perror("Failed fstat");
		close(fd);
		return 1;
	}

	r = fchownat(fd, "", uid, gid, AT_EMPTY_PATH|AT_SYMLINK_NOFOLLOW);
	if (r < 0) {
		perror("Failed chown");
		close(fd);
		return 1;
	}

	if (!S_ISLNK(sb.st_mode)) {
		r = chmod(fdpath, sb.st_mode);
		if (r < 0) {
			perror("Failed chmod");
			close(fd);
			return 1;
		}
	}

	close(fd);
	return 0;
}
*/
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

// ShiftACL updates uid and gid for file ACLs when entering/exiting a namespace
func ShiftACL(path string, shiftIds func(uid int64, gid int64) (int64, int64)) error {
	finfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if finfo.Mode()&os.ModeSymlink != 0 {
		return nil
	}

	err = shiftAclType(path, C.ACL_TYPE_ACCESS, shiftIds)
	if err != nil {
		return err
	}

	err = shiftAclType(path, C.ACL_TYPE_DEFAULT, shiftIds)
	if err != nil {
		return err
	}

	return nil
}

func shiftAclType(path string, aclType _Ctype_acl_type_t, shiftIds func(uid int64, gid int64) (int64, int64)) error {
	// Convert the path to something usable with cgo
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	// Read the current ACL set for the requested type
	acl := C.acl_get_file(cpath, aclType)
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
		ret := C.acl_set_file(cpath, aclType, newAcl)
		if ret == -1 {
			return fmt.Errorf("Failed to change ACLs on %s", path)
		}
	}

	return nil
}
