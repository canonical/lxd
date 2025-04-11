package lxd

import (
	"net/http"

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
	u := api.NewURL().Path("network-zones")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(u.String(), urls...)
}

// GetNetworkZones returns a list of Network zone structs.
func (r *ProtocolLXD) GetNetworkZones() ([]api.NetworkZone, error) {
	err := r.CheckExtension("network_dns")
	if err != nil {
		return nil, err
	}

	zones := []api.NetworkZone{}

	// Fetch the raw value.
	u := api.NewURL().Path("network-zones").WithQuery("recursion", "1")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &zones)
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
	u := api.NewURL().Path("network-zones", name)
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &zone)
	if err != nil {
		return nil, "", err
	}

	return &zone, etag, nil
}

// CreateNetworkZone defines a new Network zone using the provided struct.
func (r *ProtocolLXD) CreateNetworkZone(zone api.NetworkZonesPost) error {
	err := r.CheckExtension("network_dns")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("network-zones")
	_, _, err = r.query(http.MethodPost, u.String(), zone, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetworkZone updates the network zone to match the provided struct.
func (r *ProtocolLXD) UpdateNetworkZone(name string, zone api.NetworkZonePut, ETag string) error {
	err := r.CheckExtension("network_dns")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("network-zones", name)
	_, _, err = r.query(http.MethodPut, u.String(), zone, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkZone deletes an existing network zone.
func (r *ProtocolLXD) DeleteNetworkZone(name string) error {
	err := r.CheckExtension("network_dns")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("network-zones", name)
	_, _, err = r.query(http.MethodDelete, u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetNetworkZoneRecordNames returns a list of network zone record names.
func (r *ProtocolLXD) GetNetworkZoneRecordNames(zone string) ([]string, error) {
	err := r.CheckExtension("network_dns_records")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	u := api.NewURL().Path("network-zones", zone, "records")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(u.String(), urls...)
}

// GetNetworkZoneRecords returns a list of Network zone record structs.
func (r *ProtocolLXD) GetNetworkZoneRecords(zone string) ([]api.NetworkZoneRecord, error) {
	err := r.CheckExtension("network_dns_records")
	if err != nil {
		return nil, err
	}

	records := []api.NetworkZoneRecord{}

	// Fetch the raw value.
	u := api.NewURL().Path("network-zones", zone, "records").WithQuery("recursion", "1")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &records)
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
	u := api.NewURL().Path("network-zones", zone, "records", name)
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &record)
	if err != nil {
		return nil, "", err
	}

	return &record, etag, nil
}

// CreateNetworkZoneRecord defines a new Network zone record using the provided struct.
func (r *ProtocolLXD) CreateNetworkZoneRecord(zone string, record api.NetworkZoneRecordsPost) error {
	err := r.CheckExtension("network_dns_records")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("network-zones", zone, "records")
	_, _, err = r.query(http.MethodPost, u.String(), record, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetworkZoneRecord updates the network zone record to match the provided struct.
func (r *ProtocolLXD) UpdateNetworkZoneRecord(zone string, name string, record api.NetworkZoneRecordPut, ETag string) error {
	err := r.CheckExtension("network_dns_records")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("network-zones", zone, "records", name)
	_, _, err = r.query(http.MethodPut, u.String(), record, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkZoneRecord deletes an existing network zone record.
func (r *ProtocolLXD) DeleteNetworkZoneRecord(zone string, name string) error {
	err := r.CheckExtension("network_dns_records")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("network-zones", zone, "records", name)
	_, _, err = r.query(http.MethodDelete, u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}
