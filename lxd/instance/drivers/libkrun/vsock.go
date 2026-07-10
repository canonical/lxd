package libkrun

/*
#include "libkrun_fwd.h"
*/
import "C"

// TSIFeature holds TSI feature bits for vsock.
type TSIFeature uint32

// AddVsock adds a vsock device with optional TSI features.
func (c *Context) AddVsock(tsiFeatures TSIFeature) error {
	return check(C.krun_add_vsock(c.id, C.uint32_t(tsiFeatures)))
}

// AddVsockPort maps a guest vsock port to a host unix socket path.
func (c *Context) AddVsockPort(port uint32, filepath string) error {
	cPath := cStr(filepath)
	defer freeCStr(cPath)

	return check(C.krun_add_vsock_port(c.id, C.uint32_t(port), cPath))
}

// AddVsockPort2 maps a guest vsock port with explicit listen mode.
func (c *Context) AddVsockPort2(port uint32, filepath string, listen bool) error {
	cPath := cStr(filepath)
	defer freeCStr(cPath)

	return check(C.krun_add_vsock_port2(c.id, C.uint32_t(port), cPath, C.bool(listen)))
}
