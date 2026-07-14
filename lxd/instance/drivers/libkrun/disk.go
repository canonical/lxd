package libkrun

/*
#include "libkrun_fwd.h"
*/
import "C"

// AddDisk adds a raw disk image as a block device.
func (c *Context) AddDisk(blockID, diskPath string, readOnly bool) error {
	cBlockID := cStr(blockID)
	defer freeCStr(cBlockID)

	cDiskPath := cStr(diskPath)
	defer freeCStr(cDiskPath)

	return check(C.krun_add_disk(c.id, cBlockID, cDiskPath, C.bool(readOnly)))
}
