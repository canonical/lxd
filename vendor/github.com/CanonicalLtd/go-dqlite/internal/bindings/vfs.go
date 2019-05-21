package bindings

/*
#include <stdlib.h>

#include <sqlite3.h>
#include <dqlite.h>
*/
import "C"
import (
	"unsafe"
)

// Vfs is a Go wrapper around dqlite's in-memory VFS implementation.
type Vfs C.sqlite3_vfs

// NewVfs registers an in-memory VFS instance under the given name.
func NewVfs(name string, logger *Logger) (*Vfs, error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	if vfs := C.sqlite3_vfs_find(cname); vfs != nil {
		err := Error{Code: C.SQLITE_ERROR, Message: "vfs name already registered"}
		return nil, err
	}

	clogger := (*C.dqlite_logger)(unsafe.Pointer(logger))

	vfs := C.dqlite_vfs_create(cname, clogger)
	if vfs == nil {
		return nil, codeToError(C.SQLITE_NOMEM)
	}

	rc := C.sqlite3_vfs_register(vfs, 0)
	if rc != 0 {
		return nil, codeToError(rc)
	}

	return (*Vfs)(unsafe.Pointer(vfs)), nil
}

// Close unregisters this in-memory VFS instance.
func (v *Vfs) Close() error {
	vfs := (*C.sqlite3_vfs)(unsafe.Pointer(v))

	rc := C.sqlite3_vfs_unregister(vfs)
	if rc != 0 {
		return codeToError(rc)
	}

	C.dqlite_vfs_destroy(vfs)

	return nil
}

// Name returns the registration name of the vfs.
func (v *Vfs) Name() string {
	vfs := (*C.sqlite3_vfs)(unsafe.Pointer(v))

	return C.GoString(vfs.zName)
}

// ReadFile returns the content of the given filename.
func (v *Vfs) ReadFile(filename string) ([]byte, error) {
	vfs := (*C.sqlite3_vfs)(unsafe.Pointer(v))

	cfilename := C.CString(filename)
	defer C.free(unsafe.Pointer(cfilename))

	var buf *C.uint8_t
	var n C.size_t

	rc := C.dqlite_file_read(vfs.zName, cfilename, &buf, &n)
	if rc != 0 {
		return nil, Error{Code: int(rc)}
	}

	content := C.GoBytes(unsafe.Pointer(buf), C.int(n))

	C.sqlite3_free(unsafe.Pointer(buf))

	return content, nil
}

// WriteFile write the content of the given filename, overriding it if it
// exists.
func (v *Vfs) WriteFile(filename string, bytes []byte) error {
	if len(bytes) == 0 {
		return nil
	}

	vfs := (*C.sqlite3_vfs)(unsafe.Pointer(v))

	cfilename := C.CString(filename)
	defer C.free(unsafe.Pointer(cfilename))

	buf := (*C.uint8_t)(unsafe.Pointer(&bytes[0]))
	n := C.size_t(len(bytes))

	rc := C.dqlite_file_write(vfs.zName, cfilename, buf, n)
	if rc != 0 {
		return Error{Code: int(rc & 0xff)}
	}

	return nil
}
