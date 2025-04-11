package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetNetworkAllocations returns a list of Network allocations tied to one or several projects (e.g, for IPAM information for example).
func (r *ProtocolLXD) GetNetworkAllocations(allProjects bool) ([]api.NetworkAllocations, error) {
	err := r.CheckExtension("network_allocations")
	if err != nil {
		return nil, err
	}

	netAllocations := []api.NetworkAllocations{}

	// Fetch the raw value.
	u := api.NewURL().Path("network-allocations")
	if allProjects {
		u = u.WithQuery("all-projects", "true")
	}

	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &netAllocations)
	if err != nil {
		return nil, err
	}

	return netAllocations, nil
}
