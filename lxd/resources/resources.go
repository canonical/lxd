package resources

import (
	"fmt"

	"github.com/canonical/lxd/shared/api"
)

// GetResources returns a filled api.Resources struct ready for use by LXD.
func GetResources() (*api.Resources, error) {
	// Get CPU information
	cpu, err := GetCPU()
	if err != nil {
		return nil, fmt.Errorf("Failed retrieving CPU information: %w", err)
	}

	// Get memory information
	memory, err := GetMemory()
	if err != nil {
		return nil, fmt.Errorf("Failed retrieving memory information: %w", err)
	}

	// Get GPU information
	gpu, err := GetGPU()
	if err != nil {
		return nil, fmt.Errorf("Failed retrieving GPU information: %w", err)
	}

	// Get network card information
	network, err := GetNetwork()
	if err != nil {
		return nil, fmt.Errorf("Failed retrieving network information: %w", err)
	}

	// Get storage information
	storage, err := GetStorage()
	if err != nil {
		return nil, fmt.Errorf("Failed retrieving storage information: %w", err)
	}

	// Get USB information
	usb, err := GetUSB()
	if err != nil {
		return nil, fmt.Errorf("Failed retrieving USB information: %w", err)
	}

	// Get PCI information
	pci, err := GetPCI()
	if err != nil {
		return nil, fmt.Errorf("Failed retrieving PCI information: %w", err)
	}

	// Get system information
	system, err := GetSystem()
	if err != nil {
		return nil, fmt.Errorf("Failed retrieving system information: %w", err)
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
