package lxd

import (
	"fmt"

	"github.com/lxc/lxd/shared/api"
)

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
