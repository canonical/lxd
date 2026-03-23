package lxd

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/canonical/lxd/shared/api"
)

// GetNetworkZoneNames returns a list of network zone names.
func (r *ProtocolLXD) GetNetworkZoneNames() ([]string, error) {
	err := r.CheckExtension("network_dns")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/network-zones"
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetNetworkZones returns a list of Network zone structs.
func (r *ProtocolLXD) GetNetworkZones() ([]api.NetworkZone, error) {
	err := r.CheckExtension("network_dns")
	if err != nil {
		return nil, err
	}

	zones := []api.NetworkZone{}

	// Fetch the raw value.
	_, err = r.queryStruct(http.MethodGet, "/network-zones?recursion=1", nil, "", &zones)
	if err != nil {
		return nil, err
	}

	return zones, nil
}

// GetNetworkZonesAllProjects returns a list of network zones across all projects as NetworkZone structs.
func (r *ProtocolLXD) GetNetworkZonesAllProjects() ([]api.NetworkZone, error) {
	err := r.CheckExtension("network_zones_all_projects")
	if err != nil {
		return nil, err
	}

	zones := []api.NetworkZone{}

	u := api.NewURL().Path("network-zones").WithQuery("recursion", "1").WithQuery("all-projects", "true")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &zones)
	if err != nil {
		return nil, err
	}

	return zones, nil
}

// GetNetworkZone returns a Network zone entry for the provided name.
func (r *ProtocolLXD) GetNetworkZone(name string) (*api.NetworkZone, string, error) {
	err := r.CheckExtension("network_dns")
	if err != nil {
		return nil, "", err
	}

	zone := api.NetworkZone{}

	// Fetch the raw value.
	etag, err := r.queryStruct(http.MethodGet, "/network-zones/"+url.PathEscape(name), nil, "", &zone)
	if err != nil {
		return nil, "", err
	}

	return &zone, etag, nil
}

// CreateNetworkZone defines a new Network zone using the provided struct.
func (r *ProtocolLXD) CreateNetworkZone(zone api.NetworkZonesPost) (Operation, error) {
	err := r.CheckExtension("network_dns")
	if err != nil {
		return nil, err
	}

	var op Operation

	// Send the request.
	err = r.CheckExtension("storage_and_network_operations")
	if err != nil {
		// Fallback to older behavior without operations.
		op = noopOperation{}
		_, _, err = r.query(http.MethodPost, "/network-zones", zone, "")
	} else {
		op, _, err = r.queryOperation(http.MethodPost, "/network-zones", zone, "", true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateNetworkZone updates the network zone to match the provided struct.
func (r *ProtocolLXD) UpdateNetworkZone(name string, zone api.NetworkZonePut, ETag string) (Operation, error) {
	err := r.CheckExtension("network_dns")
	if err != nil {
		return nil, err
	}

	path := api.NewURL().Path("network-zones", name)

	var op Operation

	// Send the request.
	err = r.CheckExtension("storage_and_network_operations")
	if err != nil {
		// Fallback to older behavior without operations.
		op = noopOperation{}
		_, _, err = r.query(http.MethodPut, path.String(), zone, ETag)
	} else {
		op, _, err = r.queryOperation(http.MethodPut, path.String(), zone, ETag, true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteNetworkZone deletes an existing network zone.
func (r *ProtocolLXD) DeleteNetworkZone(name string) (Operation, error) {
	err := r.CheckExtension("network_dns")
	if err != nil {
		return nil, err
	}

	path := api.NewURL().Path("network-zones", name)

	var op Operation

	// Send the request.
	err = r.CheckExtension("storage_and_network_operations")
	if err != nil {
		// Fallback to older behavior without operations.
		op = noopOperation{}
		_, _, err = r.query(http.MethodDelete, path.String(), nil, "")
	} else {
		op, _, err = r.queryOperation(http.MethodDelete, path.String(), nil, "", true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetNetworkZoneRecordNames returns a list of network zone record names.
func (r *ProtocolLXD) GetNetworkZoneRecordNames(zone string) ([]string, error) {
	err := r.CheckExtension("network_dns_records")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := fmt.Sprintf("/network-zones/%s/records", url.PathEscape(zone))
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetNetworkZoneRecords returns a list of Network zone record structs.
func (r *ProtocolLXD) GetNetworkZoneRecords(zone string) ([]api.NetworkZoneRecord, error) {
	err := r.CheckExtension("network_dns_records")
	if err != nil {
		return nil, err
	}

	records := []api.NetworkZoneRecord{}

	// Fetch the raw value.
	_, err = r.queryStruct(http.MethodGet, fmt.Sprintf("/network-zones/%s/records?recursion=1", url.PathEscape(zone)), nil, "", &records)
	if err != nil {
		return nil, err
	}

	return records, nil
}

// GetNetworkZoneRecord returns a Network zone record entry for the provided zone and name.
func (r *ProtocolLXD) GetNetworkZoneRecord(zone string, name string) (*api.NetworkZoneRecord, string, error) {
	err := r.CheckExtension("network_dns_records")
	if err != nil {
		return nil, "", err
	}

	record := api.NetworkZoneRecord{}

	// Fetch the raw value.
	etag, err := r.queryStruct(http.MethodGet, fmt.Sprintf("/network-zones/%s/records/%s", url.PathEscape(zone), url.PathEscape(name)), nil, "", &record)
	if err != nil {
		return nil, "", err
	}

	return &record, etag, nil
}

// CreateNetworkZoneRecord defines a new Network zone record using the provided struct.
func (r *ProtocolLXD) CreateNetworkZoneRecord(zone string, record api.NetworkZoneRecordsPost) (Operation, error) {
	err := r.CheckExtension("network_dns_records")
	if err != nil {
		return nil, err
	}

	path := api.NewURL().Path("network-zones", zone, "records")

	var op Operation

	// Send the request.
	err = r.CheckExtension("storage_and_network_operations")
	if err != nil {
		// Fallback to older behavior without operations.
		op = noopOperation{}
		_, _, err = r.query(http.MethodPost, path.String(), record, "")
	} else {
		op, _, err = r.queryOperation(http.MethodPost, path.String(), record, "", true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateNetworkZoneRecord updates the network zone record to match the provided struct.
func (r *ProtocolLXD) UpdateNetworkZoneRecord(zone string, name string, record api.NetworkZoneRecordPut, ETag string) (Operation, error) {
	err := r.CheckExtension("network_dns_records")
	if err != nil {
		return nil, err
	}

	path := api.NewURL().Path("network-zones", zone, "records", name)

	var op Operation

	// Send the request.
	err = r.CheckExtension("storage_and_network_operations")
	if err != nil {
		// Fallback to older behavior without operations.
		op = noopOperation{}
		_, _, err = r.query(http.MethodPut, path.String(), record, ETag)
	} else {
		op, _, err = r.queryOperation(http.MethodPut, path.String(), record, ETag, true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteNetworkZoneRecord deletes an existing network zone record.
func (r *ProtocolLXD) DeleteNetworkZoneRecord(zone string, name string) (Operation, error) {
	err := r.CheckExtension("network_dns_records")
	if err != nil {
		return nil, err
	}

	path := api.NewURL().Path("network-zones", zone, "records", name)

	var op Operation

	// Send the request.
	err = r.CheckExtension("storage_and_network_operations")
	if err != nil {
		// Fallback to older behavior without operations.
		op = noopOperation{}
		_, _, err = r.query(http.MethodDelete, path.String(), nil, "")
	} else {
		op, _, err = r.queryOperation(http.MethodDelete, path.String(), nil, "", true)
	}

	if err != nil {
		return nil, err
	}

	return op, nil
}
