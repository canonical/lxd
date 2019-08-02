package device

import (
	"github.com/lxc/lxd/lxd/state"
)

// InstanceLoadNodeAll returns all local instance configs.
var InstanceLoadNodeAll func(s *state.State) ([]InstanceIdentifier, error)

// InstanceLoadByProjectAndName returns instance config by project and name.
var InstanceLoadByProjectAndName func(s *state.State, project, name string) (InstanceIdentifier, error)
