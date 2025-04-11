package lxd

import (
	"fmt"
	"net"
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetNetworkForwardAddresses returns a list of network forward listen addresses.
func (r *ProtocolLXD) GetNetworkForwardAddresses(networkName string) ([]string, error) {
	err := r.CheckExtension("network_forward")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	u := api.NewURL().Path("networks", networkName, "forwards")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(u.String(), urls...)
}

// GetNetworkForwards returns a list of Network forward structs.
func (r *ProtocolLXD) GetNetworkForwards(networkName string) ([]api.NetworkForward, error) {
	err := r.CheckExtension("network_forward")
	if err != nil {
		return nil, err
	}

	forwards := []api.NetworkForward{}

	// Fetch the raw value.
	u := api.NewURL().Path("networks", networkName, "forwards").WithQuery("recursion", "1")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &forwards)
	if err != nil {
		return nil, err
	}

	return forwards, nil
}

// GetNetworkForward returns a Network forward entry for the provided network and listen address.
func (r *ProtocolLXD) GetNetworkForward(networkName string, listenAddress string) (*api.NetworkForward, string, error) {
	err := r.CheckExtension("network_forward")
	if err != nil {
		return nil, "", err
	}

	forward := api.NetworkForward{}

	// Fetch the raw value.
	u := api.NewURL().Path("networks", networkName, "forwards", listenAddress)
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &forward)
	if err != nil {
		return nil, "", err
	}

	return &forward, etag, nil
}

// CreateNetworkForward defines a new network forward using the provided struct.
func (r *ProtocolLXD) CreateNetworkForward(networkName string, forward api.NetworkForwardsPost) error {
	err := r.CheckExtension("network_forward")
	if err != nil {
		return err
	}

	listenAddressIP := net.ParseIP(forward.ListenAddress)
	if listenAddressIP == nil {
		return fmt.Errorf("Invalid network forward listen address: %s", forward.ListenAddress)
	}

	if listenAddressIP.IsUnspecified() {
		err := r.CheckExtension("network_allocate_external_ips")
		if err != nil {
			return err
		}
	}

	// Send the request.
	u := api.NewURL().Path("networks", networkName, "forwards")
	_, _, err = r.query(http.MethodPost, u.String(), forward, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetworkForward updates the network forward to match the provided struct.
func (r *ProtocolLXD) UpdateNetworkForward(networkName string, listenAddress string, forward api.NetworkForwardPut, ETag string) error {
	err := r.CheckExtension("network_forward")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("networks", networkName, "forwards", listenAddress)
	_, _, err = r.query(http.MethodPut, u.String(), forward, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkForward deletes an existing network forward.
func (r *ProtocolLXD) DeleteNetworkForward(networkName string, listenAddress string) error {
	err := r.CheckExtension("network_forward")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("networks", networkName, "forwards", listenAddress)
	_, _, err = r.query(http.MethodDelete, u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}
