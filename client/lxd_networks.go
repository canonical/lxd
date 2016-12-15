package lxd

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd/shared/api"
)

// GetNetworkNames returns a list of network names
func (r *ProtocolLXD) GetNetworkNames() ([]string, error) {
	urls := []string{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/networks", nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	names := []string{}
	for _, url := range urls {
		fields := strings.Split(url, "/networks/")
		names = append(names, fields[len(fields)-1])
	}

	return names, nil
}

// GetNetworks returns a list of Network struct
func (r *ProtocolLXD) GetNetworks() ([]api.Network, error) {
	networks := []api.Network{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/networks?recursion=1", nil, "", &networks)
	if err != nil {
		return nil, err
	}

	return networks, nil
}

// GetNetwork returns a Network entry for the provided name
func (r *ProtocolLXD) GetNetwork(name string) (*api.Network, string, error) {
	network := api.Network{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/networks/%s", name), nil, "", &network)
	if err != nil {
		return nil, "", err
	}

	return &network, etag, nil
}
