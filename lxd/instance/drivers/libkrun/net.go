package libkrun

/*
#include "libkrun_fwd.h"
*/
import "C"

// NetFlag holds virtio-net interface flags.
type NetFlag uint32

// NetFeature holds virtio-net feature bits.
type NetFeature uint32

// CompatNetFeatures contains the libkrun default compatibility feature mask.
const (
	CompatNetFeatures NetFeature = C.COMPAT_NET_FEATURES
)

func macPtr(mac [6]byte, buf *[6]C.uint8_t) *C.uint8_t {
	for i, b := range mac {
		buf[i] = C.uint8_t(b)
	}

	return &buf[0]
}

// AddNetTap adds a virtio-net device backed by a host tap device.
func (c *Context) AddNetTap(tapName string, mac [6]byte, features NetFeature, flags NetFlag) error {
	cTapName := cStr(tapName)
	defer freeCStr(cTapName)

	// Safe: krun_add_net_tap copies the MAC synchronously, so macBuf may stay stack-allocated.
	var macBuf [6]C.uint8_t
	return check(C.krun_add_net_tap(
		c.id,
		cTapName,
		macPtr(mac, &macBuf),
		C.uint32_t(features),
		C.uint32_t(flags),
	))
}
