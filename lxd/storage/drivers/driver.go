package drivers

import (
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

var drivers = map[string]func(name string, path string, config map[string]string) driver{
	"dir": func(name string, path string, config map[string]string) driver {
		return &dir{name: name, path: path, config: config}
	},
}

// Create performs the initial validation and alteration of the configuration and creates the low-level storage pool, returning a Driver.
func Create(dbPool *api.StoragePool) (Driver, error) {
	if dbPool == nil {
		return nil, ErrNilValue
	}

	// Locate the driver
	_, ok := drivers[dbPool.Driver]
	if !ok {
		return nil, ErrUnknownDriver
	}

	path := shared.VarPath("storage-pools", dbPool.Name)
	d := drivers[dbPool.Driver](dbPool.Name, path, dbPool.Config)

	// Create the low level pool
	err := d.create(dbPool)
	if err != nil {
		return nil, err
	}

	return d, nil
}

// Load returns a Driver for an existing low-level storage pool.
func Load(driverName string, name string, path string, config map[string]string) (Driver, error) {
	// Locate the driver
	_, ok := drivers[driverName]
	if !ok {
		return nil, ErrUnknownDriver
	}

	d := drivers[driverName](name, path, config)

	return d, nil
}
