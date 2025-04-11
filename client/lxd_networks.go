package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetNetworkNames returns a list of network names.
func (r *ProtocolLXD) GetNetworkNames() ([]string, error) {
	err := r.CheckExtension("network")
	if err != nil {
		return nil, err
	}

	// Fetch the raw values.
	urls := []string{}
	u := api.NewURL().Path("networks")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(u.String(), urls...)
}

// GetNetworks returns a list of Network struct.
func (r *ProtocolLXD) GetNetworks() ([]api.Network, error) {
	err := r.CheckExtension("network")
	if err != nil {
		return nil, err
	}

	networks := []api.Network{}

	// Fetch the raw value
	u := api.NewURL().Path("networks").WithQuery("recursion", "1")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &networks)
	if err != nil {
		return nil, err
	}

	return networks, nil
}

// GetNetwork returns a Network entry for the provided name.
func (r *ProtocolLXD) GetNetwork(name string) (*api.Network, string, error) {
	err := r.CheckExtension("network")
	if err != nil {
		return nil, "", err
	}

	network := api.Network{}

	// Fetch the raw value
	u := api.NewURL().Path("networks", name)
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &network)
	if err != nil {
		return nil, "", err
	}

	return &network, etag, nil
}

// GetNetworkLeases returns a list of Network struct.
func (r *ProtocolLXD) GetNetworkLeases(name string) ([]api.NetworkLease, error) {
	err := r.CheckExtension("network_leases")
	if err != nil {
		return nil, err
	}

	leases := []api.NetworkLease{}

	// Fetch the raw value
	u := api.NewURL().Path("networks", name, "leases")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &leases)
	if err != nil {
		return nil, err
	}

	return leases, nil
}

// GetNetworkState returns metrics and information on the running network.
func (r *ProtocolLXD) GetNetworkState(name string) (*api.NetworkState, error) {
	err := r.CheckExtension("network_state")
	if err != nil {
		return nil, err
	}

	state := api.NetworkState{}

	// Fetch the raw value
	u := api.NewURL().Path("networks", name, "state")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &state)
	if err != nil {
		return nil, err
	}

	return &state, nil
}

// CreateNetwork defines a new network using the provided Network struct.
func (r *ProtocolLXD) CreateNetwork(network api.NetworksPost) error {
	err := r.CheckExtension("network")
	if err != nil {
		return err
	}

	// Send the request
	u := api.NewURL().Path("networks")
	_, _, err = r.query(http.MethodPost, u.String(), network, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetwork updates the network to match the provided Network struct.
func (r *ProtocolLXD) UpdateNetwork(name string, network api.NetworkPut, ETag string) error {
	err := r.CheckExtension("network")
	if err != nil {
		return err
	}

	// Send the request
	u := api.NewURL().Path("networks", name)
	_, _, err = r.query(http.MethodPut, u.String(), network, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameNetwork renames an existing network entry.
func (r *ProtocolLXD) RenameNetwork(name string, network api.NetworkPost) error {
	err := r.CheckExtension("network")
	if err != nil {
		return err
	}

	// Send the request
	u := api.NewURL().Path("networks", name)
	_, _, err = r.query(http.MethodPost, u.String(), network, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetwork deletes an existing network.
func (r *ProtocolLXD) DeleteNetwork(name string) error {
	err := r.CheckExtension("network")
	if err != nil {
		return err
	}

	// Send the request
	u := api.NewURL().Path("networks", name)
	_, _, err = r.query(http.MethodDelete, u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}
