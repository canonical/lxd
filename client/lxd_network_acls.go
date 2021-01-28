package lxd

import (
	"fmt"
	"net/url"

	"github.com/lxc/lxd/shared/api"
)

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
