package lxd

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/shared/api"
)

// GetNetworkACLNames returns a list of network ACL names.
func (r *ProtocolLXD) GetNetworkACLNames() ([]string, error) {
	if !r.HasExtension("network_acl") {
		return nil, fmt.Errorf(`The server is missing the required "network_acl" API extension`)
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/network-acls"
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetNetworkACLs returns a list of Network ACL structs.
func (r *ProtocolLXD) GetNetworkACLs() ([]api.NetworkACL, error) {
	if !r.HasExtension("network_acl") {
		return nil, fmt.Errorf(`The server is missing the required "network_acl" API extension`)
	}

	acls := []api.NetworkACL{}

	// Fetch the raw value.
	_, err := r.queryStruct("GET", "/network-acls?recursion=1", nil, "", &acls)
	if err != nil {
		return nil, err
	}

	return acls, nil
}

// GetNetworkACL returns a Network ACL entry for the provided name.
func (r *ProtocolLXD) GetNetworkACL(name string) (*api.NetworkACL, string, error) {
	if !r.HasExtension("network_acl") {
		return nil, "", fmt.Errorf(`The server is missing the required "network_acl" API extension`)
	}

	acl := api.NetworkACL{}

	// Fetch the raw value.
	etag, err := r.queryStruct("GET", fmt.Sprintf("/network-acls/%s", url.PathEscape(name)), nil, "", &acl)
	if err != nil {
		return nil, "", err
	}

	return &acl, etag, nil
}

// CreateNetworkACL defines a new network ACL using the provided struct.
func (r *ProtocolLXD) CreateNetworkACL(acl api.NetworkACLsPost) error {
	if !r.HasExtension("network_acl") {
		return fmt.Errorf(`The server is missing the required "network_acl" API extension`)
	}

	// Send the request.
	_, _, err := r.query("POST", "/network-acls", acl, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetworkACL updates the network ACL to match the provided struct.
func (r *ProtocolLXD) UpdateNetworkACL(name string, acl api.NetworkACLPut, ETag string) error {
	if !r.HasExtension("network_acl") {
		return fmt.Errorf(`The server is missing the required "network_acl" API extension`)
	}

	// Send the request.
	_, _, err := r.query("PUT", fmt.Sprintf("/network-acls/%s", url.PathEscape(name)), acl, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameNetworkACL renames an existing network ACL entry.
func (r *ProtocolLXD) RenameNetworkACL(name string, acl api.NetworkACLPost) error {
	if !r.HasExtension("network_acl") {
		return fmt.Errorf(`The server is missing the required "network_acl" API extension`)
	}

	// Send the request.
	_, _, err := r.query("POST", fmt.Sprintf("/network-acls/%s", url.PathEscape(name)), acl, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkACL deletes an existing network ACL.
func (r *ProtocolLXD) DeleteNetworkACL(name string) error {
	if !r.HasExtension("network_acl") {
		return fmt.Errorf(`The server is missing the required "network_acl" API extension`)
	}

	// Send the request.
	_, _, err := r.query("DELETE", fmt.Sprintf("/network-acls/%s", url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
