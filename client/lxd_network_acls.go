package lxd

import (
	"fmt"

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

// CreateNetworkACL defines a new network using the provided Network struct
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
