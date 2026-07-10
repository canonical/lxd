package libkrun

/*
#include "libkrun_fwd.h"
*/
import "C"

// SetVMConfig sets the number of vCPUs and amount of RAM in MiB.
func (c *Context) SetVMConfig(numVCPUs uint8, ramMiB uint32) error {
	return check(C.krun_set_vm_config(c.id, C.uint8_t(numVCPUs), C.uint32_t(ramMiB)))
}
