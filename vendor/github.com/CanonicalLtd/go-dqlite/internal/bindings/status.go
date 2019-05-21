package bindings

/*
#include <sqlite3.h>
*/
import "C"

// StatusMallocCount returns the current and highest number of memory
// allocations performed with sqlite3_malloc.
func StatusMallocCount(reset bool) (int, int, error) {
	var current C.int
	var highest C.int
	var flag C.int

	if reset {
		flag = 1
	}

	rc := C.sqlite3_status(C.SQLITE_STATUS_MALLOC_COUNT, &current, &highest, flag)
	if rc != C.SQLITE_OK {
		return -1, -1, codeToError(rc)
	}

	return int(current), int(highest), nil
}

// StatusMemoryUsed returns the current and highest allocation size.
func StatusMemoryUsed(reset bool) (int, int, error) {
	var current C.int
	var highest C.int
	var flag C.int

	if reset {
		flag = 1
	}

	rc := C.sqlite3_status(C.SQLITE_STATUS_MEMORY_USED, &current, &highest, flag)
	if rc != C.SQLITE_OK {
		return -1, -1, codeToError(rc)
	}

	return int(current), int(highest), nil
}
