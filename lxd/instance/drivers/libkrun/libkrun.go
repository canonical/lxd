package libkrun

/*
#cgo linux LDFLAGS: -ldl -lpthread
#include <stdlib.h>
#include "libkrun_fwd.h"
*/
import "C"

// Context is a libkrun configuration context used to build and start a single microVM.
type Context struct {
	id C.uint32_t
}

// CreateContext creates a new libkrun configuration context.
func CreateContext() (*Context, error) {
	ret := C.krun_create_ctx()
	if ret < 0 {
		return nil, errnoFromRet(ret)
	}

	return &Context{id: C.uint32_t(ret)}, nil
}

// Close releases the libkrun configuration context.
func (c *Context) Close() error {
	return check(C.krun_free_ctx(c.id))
}

// StartEnter starts and enters the microVM.
func (c *Context) StartEnter() error {
	return check(C.krun_start_enter(c.id))
}
