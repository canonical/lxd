package device

import (
	"fmt"

	"github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/validate"
)

func gpuValidationRules(requiredFields []string, optionalFields []string) map[string]func(value string) error {
	// Define a set of default validators for each field name.
	defaultValidators := map[string]func(value string) error{
		// lxdmeta:generate(entities=device-gpu-{physical+mdev+mig}; group=device-conf; key=vendorid)
		//
		// ---
		//  type: string
		//  shortdesc: Vendor ID of the GPU device

		// lxdmeta:generate(entities=device-gpu-sriov; group=device-conf; key=vendorid)
		//
		// ---
		//  type: string
		//  shortdesc: Vendor ID of the parent GPU device
		"vendorid": validate.Optional(validate.IsDeviceID),
		// lxdmeta:generate(entities=device-gpu-{physical+mdev+mig}; group=device-conf; key=productid)
		//
		// ---
		//  type: string
		//  shortdesc: Product ID of the GPU device

		// lxdmeta:generate(entities=device-gpu-sriov; group=device-conf; key=productid)
		//
		// ---
		//  type: string
		//  shortdesc: Product ID of the parent GPU device
		"productid": validate.Optional(validate.IsDeviceID),
		// lxdmeta:generate(entities=device-gpu-physical; group=device-conf; key=id)
		// The ID can either be the DRM card ID of the GPU device (container or VM) or a fully-qualified Container Device Interface (CDI) name (container only).
		// Here are some examples of fully-qualified CDI names:
		//
		// - `nvidia.com/gpu=0`: Instructs LXD to operate a discrete GPU (dGPU) pass-through of brand NVIDIA with the first discovered GPU on your system. You can use the `nvidia-smi` tool on your host to find out which identifier to use.
		// - `nvidia.com/gpu=1833c8b5-9aa0-5382-b784-68b7e77eb185`: Instructs LXD to operate a discrete GPU (dGPU) pass-through of brand NVIDIA with a given GPU unique identifier. This identifier should also appear with `nvidia-smi -L`.
		// - `nvidia.com/igpu=all`: Instructs LXD to pass all the host integrated GPUs (iGPU) of brand NVIDIA. The concept of an index does not currently map to iGPUs. It is possible to list them with the `nvidia-smi -L` command. A special `nvgpu` mention should appear in the generated list to indicate a device to be an iGPU.
		// - `nvidia.com/gpu=all`: Instructs LXD to pass all the host GPUs of brand NVIDIA through to the container.
		// ---
		//  type: string
		//  shortdesc: ID of the GPU device

		// lxdmeta:generate(entities=device-gpu-{mdev+mig}; group=device-conf; key=id)
		//
		// ---
		//  type: string
		//  shortdesc: DRM card ID of the GPU device

		// lxdmeta:generate(entities=device-gpu-sriov; group=device-conf; key=id)
		//
		// ---
		//  type: string
		//  shortdesc: DRM card ID of the parent GPU device
		"id": validate.IsAny,
		// lxdmeta:generate(entities=device-gpu-{physical+mdev+mig}; group=device-conf; key=pci)
		//
		// ---
		//  type: string
		//  shortdesc: PCI address of the GPU device

		// lxdmeta:generate(entities=device-gpu-sriov; group=device-conf; key=pci)
		//
		// ---
		//  type: string
		//  shortdesc: PCI address of the parent GPU device
		"pci": validate.IsPCIAddress,
		// lxdmeta:generate(entities=device-gpu-physical; group=device-conf; key=uid)
		//
		// ---
		//  type: integer
		//  defaultdesc: `0`
		//  condition: container
		//  shortdesc: UID of the device owner in the container
		"uid": unixValidUserID,
		// lxdmeta:generate(entities=device-gpu-physical; group=device-conf; key=gid)
		//
		// ---
		//  type: integer
		//  defaultdesc: `0`
		//  condition: container
		//  shortdesc: GID of the device owner in the container
		"gid": unixValidUserID,
		// lxdmeta:generate(entities=device-gpu-physical; group=device-conf; key=mode)
		//
		// ---
		//  type: integer
		//  defaultdesc: `0660`
		//  condition: container
		//  shortdesc: Mode of the device in the container
		"mode": unixValidOctalFileMode,
		// lxdmeta:generate(entities=device-gpu-mig; group=device-conf; key=mig.gi)
		//
		// ---
		//  type: integer
		//  shortdesc: Existing MIG GPU instance ID
		"mig.gi": validate.IsUint8,
		// lxdmeta:generate(entities=device-gpu-mig; group=device-conf; key=mig.ci)
		//
		// ---
		//  type: integer
		//  shortdesc: Existing MIG compute instance ID
		"mig.ci": validate.IsUint8,
		// lxdmeta:generate(entities=device-gpu-mig; group=device-conf; key=mig.uuid)
		// You can omit the `MIG-` prefix when specifying this option.
		// ---
		//  type: string
		//  shortdesc: Existing MIG device UUID
		"mig.uuid": gpuValidMigUUID,
		// lxdmeta:generate(entities=device-gpu-mdev; group=device-conf; key=mdev)
		// For example: `i915-GVTg_V5_4`
		// ---
		//  type: string
		//  defaultdesc: `0`
		//  required: yes
		//  shortdesc: The `mdev` profile to use
		"mdev": validate.IsAny,
	}

	validators := map[string]func(value string) error{}

	for _, k := range optionalFields {
		defaultValidator := defaultValidators[k]

		// If field doesn't have a known validator, it is an unknown field, skip.
		if defaultValidator == nil {
			continue
		}

		// Wrap the default validator in an empty check as field is optional.
		validators[k] = func(value string) error {
			if value == "" {
				return nil
			}

			return defaultValidator(value)
		}
	}

	// Add required fields last, that way if they are specified in both required and optional
	// field sets, the required one will overwrite the optional validators.
	for _, k := range requiredFields {
		defaultValidator := defaultValidators[k]

		// If field doesn't have a known validator, it is an unknown field, skip.
		if defaultValidator == nil {
			continue
		}

		// Wrap the default validator in a not empty check as field is required.
		validators[k] = func(value string) error {
			err := validate.IsNotEmpty(value)
			if err != nil {
				return err
			}

			return defaultValidator(value)
		}
	}

	return validators
}

// Check if the device matches the given GPU card.
// It matches based on vendorid, pci, productid or id setting of the device.
func gpuSelected(device config.Device, gpu api.ResourcesGPUCard) bool {
	return !((device["vendorid"] != "" && gpu.VendorID != device["vendorid"]) ||
		(device["pci"] != "" && gpu.PCIAddress != device["pci"]) ||
		(device["productid"] != "" && gpu.ProductID != device["productid"]) ||
		(device["id"] != "" && (gpu.DRM == nil || fmt.Sprintf("%d", gpu.DRM.ID) != device["id"])))
}
