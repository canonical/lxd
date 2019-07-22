package device

import (
	"github.com/lxc/lxd/lxd/state"
)

// InstanceLoadNodeAll returns all local instance configs.
var InstanceLoadNodeAll func(s *state.State) ([]InstanceIdentifier, error)
