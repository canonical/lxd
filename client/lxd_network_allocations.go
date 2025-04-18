package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetNetworkAllocations returns a list of Network allocations for a specific project.
func (r *ProtocolLXD) GetNetworkAllocations() ([]api.NetworkAllocations, error) {
	err := r.CheckExtension("network_allocations")
	if err != nil {
		return nil, err
	}

	netAllocations := []api.NetworkAllocations{}

	// Fetch the raw value.
	_, err = r.queryStruct(http.MethodGet, api.NewURL().Path("network-allocations").String(), nil, "", &netAllocations)
	if err != nil {
		return nil, err
	}

	return netAllocations, nil
}

// GetNetworkAllocationsAllProjects returns a list of Network allocations across all projects.
func (r *ProtocolLXD) GetNetworkAllocationsAllProjects() ([]api.NetworkAllocations, error) {
	err := r.CheckExtension("network_allocations")
	if err != nil {
		return nil, err
	}

	netAllocations := []api.NetworkAllocations{}

	// Fetch the raw value.
	_, err = r.queryStruct("GET", api.NewURL().Path("network-allocations").WithQuery("all-projects", "true").String(), nil, "", &netAllocations)
	if err != nil {
		return nil, err
	}

	return netAllocations, nil
}
