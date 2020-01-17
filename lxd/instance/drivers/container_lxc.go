package drivers

import (
	"github.com/lxc/lxd/lxd/db"
	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared/api"
)

// LXCLoad is used to link containerLXCLoad from main package, it is temporary export until such time
// as containerLXC can be moved into this package.
var LXCLoad func(s *state.State, args db.InstanceArgs, profiles []api.Profile) (instance.Instance, error)

// LXCInstantiate is used to link containerLXCInstantiate from main package, it is temporary export until such
// time as containerLXC can be moved into this package.
var LXCInstantiate func(s *state.State, args db.InstanceArgs, expandedDevices deviceConfig.Devices) instance.Instance

// LXCCreate is used to link containerLXCCreate from main package, it is temporary export until such time
// as containerLXC can be moved into this package.
var LXCCreate func(s *state.State, args db.InstanceArgs) (instance.Instance, error)
