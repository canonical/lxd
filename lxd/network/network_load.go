package network

import (
	"github.com/lxc/lxd/lxd/state"
)

var drivers = map[string]func() Network{
	"bridge": func() Network { return &bridge{} },
}

// LoadByName loads the network info from the database by name.
func LoadByName(s *state.State, name string) (Network, error) {
	id, netInfo, err := s.Cluster.GetNetwork(name)
	if err != nil {
		return nil, err
	}

	driverFunc, ok := drivers[netInfo.Type]
	if !ok {
		return nil, ErrUnknownDriver
	}

	n := driverFunc()
	n.init(s, id, name, netInfo.Type, netInfo.Description, netInfo.Config)

	return n, nil
}

// Validate validates the supplied network configuration for the specified network type.
func Validate(name string, netType string, config map[string]string) error {
	driverFunc, ok := drivers[netType]
	if !ok {
		return ErrUnknownDriver
	}

	err := ValidNetworkName(name)
	if err != nil {
		return err
	}

	n := driverFunc()
	n.init(nil, 0, name, netType, "", config)
	return n.Validate(config)
}
