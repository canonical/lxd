package resources

import (
	"github.com/lxc/lxd/shared/api"
)

// GetResources returns a filled api.Resources struct ready for use by LXD
func GetResources() (*api.Resources, error) {
	// Build the final struct
	resources := api.Resources{}

	return &resources, nil
}
