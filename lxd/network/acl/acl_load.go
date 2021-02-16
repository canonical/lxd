package acl

import (
	"fmt"

	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
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
	acl.init(s, -1, projectName, nil)

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

// Exists checks the ACL name(s) provided exists in the project.
// If multiple names are provided, also checks that duplicate names aren't specified in the list.
func Exists(s *state.State, projectName string, name ...string) error {
	existingACLNames, err := s.Cluster.GetNetworkACLs(projectName)
	if err != nil {
		return err
	}

	checkedACLNames := make(map[string]struct{}, len(name))

	for _, aclName := range name {
		if !shared.StringInSlice(aclName, existingACLNames) {
			return fmt.Errorf("Network ACL %q does not exist", aclName)
		}

		_, found := checkedACLNames[aclName]
		if found {
			return fmt.Errorf("Network ACL %q specified multiple times", aclName)
		}

		checkedACLNames[aclName] = struct{}{}
	}

	return nil
}

// UsedBy finds all networks and instance NICs that use an ACL and executes usageFunc with info about the usage.
func UsedBy(s *state.State, projectName string, aclName string, usageFunc func(usageType string, projectName string, name string, config map[string]string) error) error {
	return s.Cluster.InstanceList(nil, func(inst db.Instance, p api.Project, profiles []api.Profile) error {
		// Get the instance's effective network project name.
		instNetworkProject := project.NetworkProjectFromRecord(&p)

		// Skip instances who's effective network project doesn't match this Network ACL's project.
		if instNetworkProject != projectName {
			return nil
		}

		devices := db.ExpandInstanceDevices(deviceConfig.NewDevices(inst.Devices), profiles).CloneNative()

		// Iterate through each of the instance's devices, looking for NICs that are linked this ACL.
		for _, devConfig := range devices {
			// Only NICs linked to managed networks can use network ACLs.
			if devConfig["type"] != "nic" || devConfig["network"] == "" {
				continue
			}

			nicACLNames := util.SplitNTrimSpace(devConfig["security.acls"], ",", -1, true)
			for _, nicACLName := range nicACLNames {
				if nicACLName == aclName {
					err := usageFunc("instance", inst.Project, inst.Name, devConfig)
					if err != nil {
						return err
					}
				}
			}
		}

		return nil
	})
}
