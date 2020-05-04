package network

import (
	"github.com/lxc/lxd/lxd/state"
)

// LoadByName loads the network info from the database by name.
func LoadByName(s *state.State, name string) (*Network, error) {
	id, dbInfo, err := s.Cluster.GetNetwork(name)
	if err != nil {
		return nil, err
	}

	n := &Network{
		state:       s,
		id:          id,
		name:        name,
		description: dbInfo.Description,
		config:      dbInfo.Config,
	}

	return n, nil
}
