package lxd

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/shared/api"
)

// GetNetworkZoneNames returns a list of network zone names.
func (r *ProtocolLXD) GetNetworkZoneNames() ([]string, error) {
	if !r.HasExtension("network_dns") {
		return nil, fmt.Errorf(`The server is missing the required "network_dns" API extension`)
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/network-zones"
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetNetworkZones returns a list of Network zone structs.
func (r *ProtocolLXD) GetNetworkZones() ([]api.NetworkZone, error) {
	if !r.HasExtension("network_dns") {
		return nil, fmt.Errorf(`The server is missing the required "network_dns" API extension`)
	}

	zones := []api.NetworkZone{}

	// Fetch the raw value.
	_, err := r.queryStruct("GET", "/network-zones?recursion=1", nil, "", &zones)
	if err != nil {
		return nil, err
	}

	return zones, nil
}

// GetNetworkZone returns a Network zone entry for the provided name.
func (r *ProtocolLXD) GetNetworkZone(name string) (*api.NetworkZone, string, error) {
	if !r.HasExtension("network_dns") {
		return nil, "", fmt.Errorf(`The server is missing the required "network_dns" API extension`)
	}

	zone := api.NetworkZone{}

	// Fetch the raw value.
	etag, err := r.queryStruct("GET", fmt.Sprintf("/network-zones/%s", url.PathEscape(name)), nil, "", &zone)
	if err != nil {
		return nil, "", err
	}

	return &zone, etag, nil
}

// CreateNetworkZone defines a new Network zone using the provided struct.
func (r *ProtocolLXD) CreateNetworkZone(zone api.NetworkZonesPost) error {
	if !r.HasExtension("network_dns") {
		return fmt.Errorf(`The server is missing the required "network_dns" API extension`)
	}

	// Send the request.
	_, _, err := r.query("POST", "/network-zones", zone, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetworkZone updates the network zone to match the provided struct.
func (r *ProtocolLXD) UpdateNetworkZone(name string, zone api.NetworkZonePut, ETag string) error {
	if !r.HasExtension("network_dns") {
		return fmt.Errorf(`The server is missing the required "network_dns" API extension`)
	}

	// Send the request.
	_, _, err := r.query("PUT", fmt.Sprintf("/network-zones/%s", url.PathEscape(name)), zone, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkZone deletes an existing network zone.
func (r *ProtocolLXD) DeleteNetworkZone(name string) error {
	if !r.HasExtension("network_dns") {
		return fmt.Errorf(`The server is missing the required "network_dns" API extension`)
	}

	// Send the request.
	_, _, err := r.query("DELETE", fmt.Sprintf("/network-zones/%s", url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
