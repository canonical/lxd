//go:build !cgo || armhf || arm || arm32

package cdi

import (
	"errors"
)

// MIGDeviceUUID is not supported on this platform.
func MIGDeviceUUID(pciAddress string, gi, ci int) (string, error) {
	return "", errors.New("MIG NVML operations not supported on this platform")
}
