package device

import (
	"github.com/lxc/lxd/lxd/device/config"
)

// InstanceIdentifier is an interface that allows us to identify an Instance and its properties.
// It is intended that this interface be entirely comprised of functions which cannot be blocking
// independent of when they're called in the instance lifecycle.
type InstanceIdentifier interface {
	Name() string
	Type() string
	Project() string
	DevicesPath() string
	ExpandedConfig() map[string]string
	ExpandedDevices() config.Devices
}
