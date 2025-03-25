//go:build linux && cgo

package validate

/*
#include "../../lxd/include/config.h"
#include "../../lxd/include/memory_utils.h"
#include "../../lxd/include/mount_utils.h"

static int is_bpf_delegate_option_value_valid(char *option, char *value)
{
	__do_close int fs_fd = -EBADF;
	int ret;

	fs_fd = lxd_fsopen("bpf", FSOPEN_CLOEXEC);
	if (fs_fd < 0) {
		return 2;
	}

	ret = lxd_fsconfig(fs_fd, FSCONFIG_SET_STRING, option, value, 0);
	if (ret < 0) {
		return 1;
	}

	return 0;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// IsBPFDelegationOption validates a BPF Token delegation option.
func IsBPFDelegationOption(delegateOption string) func(value string) error {
	return func(value string) error {
		cdelegateOption := C.CString("delegate_" + delegateOption)
		defer C.free(unsafe.Pointer(cdelegateOption))

		cvalue := C.CString(value)
		defer C.free(unsafe.Pointer(cvalue))

		r := C.is_bpf_delegate_option_value_valid(cdelegateOption, cvalue)
		if r != 0 {
			return fmt.Errorf("Invalid %s option value: %s", delegateOption, value)
		}

		return nil
	}
}
