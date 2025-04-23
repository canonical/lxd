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
	uri := "/network-allocations"
	if allProjects {
		uri += "?all-projects=true"
	}

	// Fetch the raw value.
	_, err = r.queryStruct(http.MethodGet, uri, nil, "", &netAllocations)
	if err != nil {
		return nil, err
	}

	return netAllocations, nil
}
