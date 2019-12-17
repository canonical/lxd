package drivers

import (
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared/logger"
)

var drivers = map[string]func() driver{
	"dir":    func() driver { return &dir{} },
	"cephfs": func() driver { return &cephfs{} },
}

// Load returns a Driver for an existing low-level storage pool.
func Load(state *state.State, driverName string, name string, config map[string]string, logger logger.Logger, volIDFunc func(volType VolumeType, volName string) (int64, error), commonVolRulesFunc func(vol Volume) map[string]func(string) error) (Driver, error) {
	// Locate the driver loader.
	driverFunc, ok := drivers[driverName]
	if !ok {
		return nil, ErrUnknownDriver
	}

	d := driverFunc()
	d.init(state, name, config, logger, volIDFunc, commonVolRulesFunc)

	err := d.load()
	if err != nil {
		return nil, err
	}

	return d, nil
}

// SupportedDrivers returns a list of supported storage drivers.
func SupportedDrivers() []Info {
	supportedDrivers := []Info{}

	for driverName := range drivers {
		driver, err := Load(nil, driverName, "", nil, nil, nil, nil)
		if err != nil {
			continue
		}

		supportedDrivers = append(supportedDrivers, driver.Info())
	}

	return supportedDrivers
}
