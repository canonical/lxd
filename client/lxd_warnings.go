package lxd

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/shared/api"
)

// Warning handling functions

// GetWarningUUIDs returns a list of operation uuids.
func (r *ProtocolLXD) GetWarningUUIDs() ([]string, error) {
	err := r.CheckExtension("warnings")
	if err != nil {
		return nil, err
	}

	// Fetch the raw values.
	urls := []string{}
	baseURL := "/warnings"
	_, err = r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetWarnings returns a list of warnings.
func (r *ProtocolLXD) GetWarnings() ([]api.Warning, error) {
	err := r.CheckExtension("warnings")
	if err != nil {
		return nil, err
	}

	warnings := []api.Warning{}

	_, err = r.queryStruct("GET", "/warnings?recursion=1", nil, "", &warnings)
	if err != nil {
		return nil, err
	}

	return warnings, nil
}

// GetWarning returns the warning with the given UUID.
func (r *ProtocolLXD) GetWarning(UUID string) (*api.Warning, string, error) {
	err := r.CheckExtension("warnings")
	if err != nil {
		return nil, "", err
	}

	warning := api.Warning{}

	etag, err := r.queryStruct("GET", fmt.Sprintf("/warnings/%s", url.PathEscape(UUID)), nil, "", &warning)
	if err != nil {
		return nil, "", err
	}

	return &warning, etag, nil
}

// UpdateWarning updates the warning with the given UUID.
func (r *ProtocolLXD) UpdateWarning(UUID string, warning api.WarningPut, ETag string) error {
	err := r.CheckExtension("warnings")
	if err != nil {
		return err
	}

	// Send the request
	_, _, err = r.query("PUT", fmt.Sprintf("/warnings/%s", url.PathEscape(UUID)), warning, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteWarning deletes the provided warning.
func (r *ProtocolLXD) DeleteWarning(UUID string) error {
	err := r.CheckExtension("warnings")
	if err != nil {
		return err
	}

	// Send the request
	_, _, err = r.query("DELETE", fmt.Sprintf("/warnings/%s", url.PathEscape(UUID)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
