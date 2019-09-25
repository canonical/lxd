package device

import (
	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance/instancetype"
)

// InstanceIdentifier is an interface that allows us to identify an Instance and its properties.
// It is intended that this interface be entirely comprised of functions which cannot be blocking
// independent of when they're called in the instance lifecycle.
type InstanceIdentifier interface {
	Name() string
	Type() instancetype.Type
	Project() string
	DevicesPath() string
	RootfsPath() string
	LogPath() string
	ExpandedConfig() map[string]string
	LocalDevices() config.Devices
	ExpandedDevices() config.Devices
	DeviceEventHandler(*RunConfig) error
}
