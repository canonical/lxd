package libkrun

/*
#include "libkrun_fwd.h"
*/
import "C"

// KernelFormat identifies kernel image format.
type KernelFormat uint32

// KernelFormat* values map to libkrun kernel image formats.
const (
	KernelFormatRaw       KernelFormat = C.KRUN_KERNEL_FORMAT_RAW
	KernelFormatELF       KernelFormat = C.KRUN_KERNEL_FORMAT_ELF
	KernelFormatPEGZ      KernelFormat = C.KRUN_KERNEL_FORMAT_PE_GZ
	KernelFormatImageBZ2  KernelFormat = C.KRUN_KERNEL_FORMAT_IMAGE_BZ2
	KernelFormatImageGZ   KernelFormat = C.KRUN_KERNEL_FORMAT_IMAGE_GZ
	KernelFormatImageZstd KernelFormat = C.KRUN_KERNEL_FORMAT_IMAGE_ZSTD
)

// SetKernel sets kernel image, format, and optional initramfs/cmdline.
func (c *Context) SetKernel(kernelPath string, format KernelFormat, initramfs, cmdline string) error {
	cKernel := cStr(kernelPath)
	defer freeCStr(cKernel)

	cInitramfs := optCStr(initramfs)
	defer freeCStr(cInitramfs)

	cCmdline := optCStr(cmdline)
	defer freeCStr(cCmdline)

	return check(C.krun_set_kernel(c.id, cKernel, C.uint32_t(format), cInitramfs, cCmdline))
}
