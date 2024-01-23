package lxd

import (
	"fmt"
	"net/url"

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
	baseURL := "/networks"
	_, err = r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetNetworks returns a list of Network struct.
func (r *ProtocolLXD) GetNetworks() ([]api.Network, error) {
	err := r.CheckExtension("network")
	if err != nil {
		return nil, err
	}

	networks := []api.Network{}

	// Fetch the raw value
	_, err = r.queryStruct("GET", "/networks?recursion=1", nil, "", &networks)
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
	etag, err := r.queryStruct("GET", fmt.Sprintf("/networks/%s", url.PathEscape(name)), nil, "", &network)
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
	_, err = r.queryStruct("GET", fmt.Sprintf("/networks/%s/leases", url.PathEscape(name)), nil, "", &leases)
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
	_, err = r.queryStruct("GET", fmt.Sprintf("/networks/%s/state", url.PathEscape(name)), nil, "", &state)
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
	_, _, err = r.query("POST", "/networks", network, "")
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
	_, _, err = r.query("PUT", fmt.Sprintf("/networks/%s", url.PathEscape(name)), network, ETag)
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
	_, _, err = r.query("POST", fmt.Sprintf("/networks/%s", url.PathEscape(name)), network, "")
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
	_, _, err = r.query("DELETE", fmt.Sprintf("/networks/%s", url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
