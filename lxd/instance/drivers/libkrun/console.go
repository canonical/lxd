package libkrun

/*
#include "libkrun_fwd.h"
*/
import "C"

// AddVirtioConsoleDefault adds a default virtio-console device wired to host fds.
func (c *Context) AddVirtioConsoleDefault(inputFD, outputFD, errFD int) error {
	return check(C.krun_add_virtio_console_default(
		c.id,
		C.int(inputFD),
		C.int(outputFD),
		C.int(errFD),
	))
}

// AddVirtioConsoleMultiport adds a multiport console and returns its console ID.
func (c *Context) AddVirtioConsoleMultiport() (uint32, error) {
	ret := C.krun_add_virtio_console_multiport(c.id)
	if ret < 0 {
		return 0, errnoFromRet(ret)
	}

	return uint32(ret), nil
}

// AddConsolePortInout adds a named bidirectional port to a multiport console.
func (c *Context) AddConsolePortInout(consoleID uint32, name string, inputFD, outputFD int) error {
	cName := cStr(name)
	defer freeCStr(cName)

	return check(C.krun_add_console_port_inout(
		c.id,
		C.uint32_t(consoleID),
		cName,
		C.int(inputFD),
		C.int(outputFD),
	))
}
