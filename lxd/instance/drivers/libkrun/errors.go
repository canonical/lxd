package libkrun

/*
#include <stdint.h>
#include "libkrun_fwd.h"

const char *goKrunLoaderLastError(void);
*/
import "C"

import (
	"syscall"
)

// loaderErrorCode mirrors the C sentinel used by the local runtime loader wrapper.
const loaderErrorCode int32 = int32(C.KRUN_LOADER_ERR)

// LoaderError reports runtime libkrun loader failures (dlopen/dlsym).
type LoaderError struct {
	message string
}

func (e LoaderError) Error() string {
	if e.message == "" {
		return "libkrun loader failure"
	}

	return e.message
}

// Errno wraps a negative libkrun return code as a POSIX errno.
type Errno syscall.Errno

func (e Errno) Error() string {
	return syscall.Errno(e).Error()
}

func errnoFromRet(ret C.int32_t) error {
	return errnoFromCode(int32(ret))
}

func check(ret C.int32_t) error {
	return checkCode(int32(ret))
}

func errnoFromCode(code int32) error {
	if code == loaderErrorCode {
		return LoaderError{message: C.GoString(C.goKrunLoaderLastError())}
	}

	return Errno(-code)
}

func checkCode(code int32) error {
	if code < 0 {
		return errnoFromCode(code)
	}

	return nil
}
