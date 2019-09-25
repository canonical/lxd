package instance

import (
	"github.com/lxc/lxd/lxd/device"
	"github.com/lxc/lxd/lxd/state"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

// DevicesRegister calls the Register() function on all supported devices so they receive events.
func DevicesRegister(s *state.State) {
	instances, err := InstanceLoadNodeAll(s)
	if err != nil {
		logger.Error("Problem loading containers list", log.Ctx{"err": err})
		return
	}

	for _, instanceIf := range instances {
		c, ok := instanceIf.(*ContainerLXC)
		if !ok {
			logger.Errorf("Instance is not container type")
			continue
		}

		if !c.IsRunning() {
			continue
		}

		devices := c.ExpandedDevices()
		for _, dev := range devices.Sorted() {
			d, _, err := c.deviceLoad(dev.Name, dev.Config)
			if err == device.ErrUnsupportedDevType {
				continue
			}

			if err != nil {
				logger.Error("Failed to load device to register", log.Ctx{"err": err, "container": c.Name(), "device": dev.Name})
				continue
			}

			// Check whether device wants to register for any events.
			err = d.Register()
			if err != nil {
				logger.Error("Failed to register device", log.Ctx{"err": err, "container": c.Name(), "device": dev.Name})
				continue
			}
		}
	}
}
