package resources

import (
	"github.com/pkg/errors"

	"github.com/grant-he/lxd/shared/api"
)

// GetResources returns a filled api.Resources struct ready for use by LXD
func GetResources() (*api.Resources, error) {
	// Get CPU information
	cpu, err := GetCPU()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to retrieve CPU information")
	}

	// Get memory information
	memory, err := GetMemory()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to retrieve memory information")
	}

	// Get GPU information
	gpu, err := GetGPU()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to retrieve GPU information")
	}

	// Get network card information
	network, err := GetNetwork()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to retrieve network information")
	}

	// Get storage information
	storage, err := GetStorage()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to retrieve storage information")
	}

	// Get USB information
	usb, err := GetUSB()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to retrieve USB information")
	}

	// Get PCI information
	pci, err := GetPCI()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to retrieve PCI information")
	}

	// Get system information
	system, err := GetSystem()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to retrieve system information")
	}

	// Build the final struct
	resources := api.Resources{
		CPU:     *cpu,
		Memory:  *memory,
		GPU:     *gpu,
		Network: *network,
		Storage: *storage,
		USB:     *usb,
		PCI:     *pci,
		System:  *system,
	}

	return &resources, nil
}
