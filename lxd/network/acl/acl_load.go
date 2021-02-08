package acl

import (
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared/api"
)

// LoadByName loads and initialises a Network ACL from the database by project and name.
func LoadByName(s *state.State, projectName string, name string) (NetworkACL, error) {
	id, aclInfo, err := s.Cluster.GetNetworkACL(projectName, name)
	if err != nil {
		return nil, err
	}

	var acl NetworkACL = &common{} // Only a single driver currently.
	acl.init(s, id, projectName, aclInfo)

	return acl, nil
}

// Create validates supplied record and creates new Network ACL record in the database.
func Create(s *state.State, projectName string, aclInfo *api.NetworkACLsPost) error {
	var acl NetworkACL = &common{} // Only a single driver currently.

	err := acl.validateName(aclInfo.Name)
	if err != nil {
		return err
	}

	err = acl.validateConfig(&aclInfo.NetworkACLPut)
	if err != nil {
		return err
	}

	// Insert DB record.
	_, err = s.Cluster.CreateNetworkACL(projectName, aclInfo)
	if err != nil {
		return err
	}

	return nil
}
