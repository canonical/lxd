package resources

import (
	"github.com/lxc/lxd/shared/api"
)

// GetResources returns a filled api.Resources struct ready for use by LXD
func GetResources() (*api.Resources, error) {
	// Get CPU information
	cpu, err := GetCPU()
	if err != nil {
		return nil, err
	}

	// Build the final struct
	resources := api.Resources{
		CPU: *cpu,
	}

	return &resources, nil
}
