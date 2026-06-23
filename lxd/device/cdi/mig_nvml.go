//go:build cgo && !armhf && !arm && !arm32

package cdi

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// MIGDeviceUUID returns the NVML UUID of the MIG device that matches the given GPU instance
// ID (gi) and compute instance ID (ci) on the GPU at the specified PCI address.
// The returned UUID is in the format "MIG-GPU-<uuid>/<gi>/<ci>" as reported by NVML,
// and is suitable for use as the UUID part of a "nvidia.com/mig=<uuid>" CDI identifier.
//
// Concurrent calls are safe, go-nvml functions are mutex-protected.
func MIGDeviceUUID(pciAddress string, gi, ci int) (string, error) {
	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		return "", fmt.Errorf("Failed initializing NVML: %v", ret)
	}

	defer func() { _ = nvml.Shutdown() }()

	device, ret := nvml.DeviceGetHandleByPciBusId(pciAddress)
	if ret != nvml.SUCCESS {
		return "", fmt.Errorf("Failed getting GPU handle for PCI address %q: %v", pciAddress, ret)
	}

	currentMode, _, ret := device.GetMigMode()
	if ret != nvml.SUCCESS {
		return "", fmt.Errorf("Failed getting MIG mode for GPU at %q: %v", pciAddress, ret)
	}

	if currentMode != nvml.DEVICE_MIG_ENABLE {
		return "", fmt.Errorf("MIG mode is not enabled on GPU at %q", pciAddress)
	}

	count, ret := device.GetMaxMigDeviceCount()
	if ret != nvml.SUCCESS {
		return "", fmt.Errorf("Failed getting MIG device count for GPU at %q: %v", pciAddress, ret)
	}

	for i := 0; i < count; i++ {
		migDevice, ret := device.GetMigDeviceHandleByIndex(i)
		if ret == nvml.ERROR_NOT_FOUND || ret == nvml.ERROR_INVALID_ARGUMENT {
			continue
		}

		if ret != nvml.SUCCESS {
			return "", fmt.Errorf("Failed getting MIG device handle at index %d on GPU at %q: %v", i, pciAddress, ret)
		}

		giID, ret := migDevice.GetGpuInstanceId()
		if ret != nvml.SUCCESS {
			return "", fmt.Errorf("Failed getting GPU instance ID for MIG device %d on GPU at %q: %v", i, pciAddress, ret)
		}

		ciID, ret := migDevice.GetComputeInstanceId()
		if ret != nvml.SUCCESS {
			return "", fmt.Errorf("Failed getting compute instance ID for MIG device %d on GPU at %q: %v", i, pciAddress, ret)
		}

		if giID != gi || ciID != ci {
			continue
		}

		uuid, ret := migDevice.GetUUID()
		if ret != nvml.SUCCESS {
			return "", fmt.Errorf("Failed getting UUID for MIG device %d on GPU at %q: %v", i, pciAddress, ret)
		}

		return uuid, nil
	}

	return "", fmt.Errorf("No MIG device with gi=%d ci=%d found on GPU at %q", gi, ci, pciAddress)
}
