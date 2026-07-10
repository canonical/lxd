package libkrun

/*
#include "libkrun_fwd.h"
*/
import "C"

// AddVirtioFS3 adds a virtio-fs export with optional read-only mode.
func (c *Context) AddVirtioFS3(tag, path string, shmSize uint64, readOnly bool) error {
	cTag := cStr(tag)
	defer freeCStr(cTag)

	cPath := cStr(path)
	defer freeCStr(cPath)

	return check(C.krun_add_virtiofs3(c.id, cTag, cPath, C.uint64_t(shmSize), C.bool(readOnly)))
}
