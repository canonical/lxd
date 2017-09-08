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

	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	acl := C.acl_get_file(cpath, C.ACL_TYPE_ACCESS)
	if acl == nil {
		return nil
	}
	defer C.acl_free(unsafe.Pointer(acl))

	for entryId := C.ACL_FIRST_ENTRY; ; entryId = C.ACL_NEXT_ENTRY {
		var ent C.acl_entry_t
		var tag C.acl_tag_t
		updateACL := false

		ret := C.acl_get_entry(acl, C.int(entryId), &ent)
		if ret != 1 {
			break
		}

		ret = C.acl_get_tag_type(ent, &tag)
		if ret == -1 {
			return fmt.Errorf("Failed to change ACLs on %s", path)
		}

		idp := (*C.id_t)(C.acl_get_qualifier(ent))
		if idp == nil {
			continue
		}

		var newId int64
		switch tag {
		case C.ACL_USER:
			newId, _ = shiftIds((int64)(*idp), -1)
			updateACL = true

		case C.ACL_GROUP:
			_, newId = shiftIds(-1, (int64)(*idp))
			updateACL = true
		}

		if updateACL {
			ret = C.acl_set_qualifier(ent, unsafe.Pointer(&newId))
			if ret == -1 {
				return fmt.Errorf("Failed to change ACLs on %s", path)
			}
			ret = C.acl_set_file(cpath, C.ACL_TYPE_ACCESS, acl)
			if ret == -1 {
				return fmt.Errorf("Failed to change ACLs on %s", path)
			}
		}
	}
	return nil
}
