package network

import (
	"fmt"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared/api"
)

var drivers = map[string]func() Network{
	"bridge":  func() Network { return &bridge{} },
	"macvlan": func() Network { return &macvlan{} },
	"sriov":   func() Network { return &sriov{} },
	"ovn":     func() Network { return &ovn{} },
}

// LoadByName loads the network info from the database by name.
func LoadByName(s *state.State, project string, name string) (Network, error) {
	id, netInfo, err := s.Cluster.GetNetworkInAnyState(project, name)
	if err != nil {
		return nil, err
	}

	driverFunc, ok := drivers[netInfo.Type]
	if !ok {
		return nil, ErrUnknownDriver
	}

	n := driverFunc()
	n.init(s, id, project, name, netInfo.Type, netInfo.Description, netInfo.Config, netInfo.Status)

	return n, nil
}

// ValidateNameAndProject validates the supplied network name and project support for the specified network type.
func ValidateNameAndProject(name string, networkProjectName string, netType string) error {
	driverFunc, ok := drivers[netType]
	if !ok {
		return ErrUnknownDriver
	}

	n := driverFunc()
	n.init(nil, 0, networkProjectName, name, netType, "", nil, "Unknown")

	err := n.ValidateName(name)
	if err != nil {
		return errors.Wrapf(err, "Network name invalid")
	}

	if networkProjectName != project.Default && !n.Info().Projects {
		return fmt.Errorf("Network type does not support non-default projects")
	}

	return nil
}

// Validate validates the supplied network name and configuration for the specified network type.
func Validate(name string, netType string, config map[string]string) error {
	driverFunc, ok := drivers[netType]
	if !ok {
		return ErrUnknownDriver
	}

	n := driverFunc()
	n.init(nil, 0, project.Default, name, netType, "", config, "Unknown")

	err := n.ValidateName(name)
	if err != nil {
		return errors.Wrapf(err, "Network name invalid")
	}

	return n.Validate(config)
}

// FillConfig populates the supplied api.NetworkPost with automatically populated values.
func FillConfig(req *api.NetworksPost) error {
	driverFunc, ok := drivers[req.Type]
	if !ok {
		return ErrUnknownDriver
	}

	n := driverFunc()
	n.init(nil, 0, project.Default, req.Name, req.Type, req.Description, req.Config, "Unknown")

	err := n.fillConfig(req.Config)
	if err != nil {
		return err
	}

	return nil
}
