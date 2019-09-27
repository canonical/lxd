package device

import (
	"fmt"
	"sync"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/state"
)

// InstanceLoadNodeAll returns all local instance configs.
var InstanceLoadNodeAll func(s *state.State) ([]Instance, error)

// InstanceLoadByProjectAndName returns instance config by project and name.
var InstanceLoadByProjectAndName func(s *state.State, project, name string) (Instance, error)

// reservedDevicesMutex used to coordinate access for checking reserved devices.
var reservedDevicesMutex sync.Mutex

// instanceGetReservedDevices returns a map of host device names that have been used by devices in
// other instances on the local node. Used for selecting physical and SR-IOV VF devices.
func instanceGetReservedDevices(s *state.State, m deviceConfig.Device) (map[string]struct{}, error) {
	reservedDevicesMutex.Lock()
	defer reservedDevicesMutex.Unlock()

	instances, err := InstanceLoadNodeAll(s)
	if err != nil {
		return nil, err
	}

	// Build a unique set of reserved host network devices we cannot use.
	reservedDevices := map[string]struct{}{}
	for _, instance := range instances {
		devices := instance.ExpandedDevices()
		config := instance.ExpandedConfig()
		for devName, devConfig := range devices {
			// Record all parent devices, these are not eligible for use as physical or
			// SR-IOV parents for selecting VF devices.
			parent := devConfig["parent"]
			reservedDevices[parent] = struct{}{}

			// If the device on another instance has the same device type as us, and has
			// the same parent as us, and a non-empty host_name, then mark that
			// host_name as reserved, as that device is using it as a SR-IOV VF.
			if devConfig["type"] == m["type"] && parent == m["parent"] {
				hostName := config[fmt.Sprintf("volatile.%s.host_name", devName)]
				if hostName != "" {
					reservedDevices[hostName] = struct{}{}
				}
			}
		}
	}

	return reservedDevices, nil
}
