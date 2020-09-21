package network

import (
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/state"
)

var drivers = map[string]func() Network{
	"bridge": func() Network { return &bridge{} },
}

// LoadByType loads a network by driver type.
func LoadByType(driverType string) (Type, error) {
	driverFunc, ok := drivers[driverType]
	if !ok {
		return nil, ErrUnknownDriver
	}

	n := driverFunc()

	return n, nil
}

// LoadByName loads an instantiated network from the database by name.
func LoadByName(s *state.State, name string) (Network, error) {
	id, netInfo, err := s.Cluster.GetNetworkInAnyState(name)
	if err != nil {
		return nil, err
	}

	driverFunc, ok := drivers[netInfo.Type]
	if !ok {
		return nil, ErrUnknownDriver
	}

	n := driverFunc()
	n.init(s, id, name, netInfo.Type, netInfo.Description, netInfo.Config, netInfo.Status)

	return n, nil
}

// Validate validates the supplied network name and configuration for the specified network type.
func Validate(name string, netType string, config map[string]string) error {
	driverFunc, ok := drivers[netType]
	if !ok {
		return ErrUnknownDriver
	}

	n := driverFunc()
	n.init(nil, 0, name, netType, "", config, "Unknown")

	err := n.ValidateName(name)
	if err != nil {
		return errors.Wrapf(err, "Network name invalid")
	}

	return n.Validate(config)
}
