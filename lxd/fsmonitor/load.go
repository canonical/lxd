package fsmonitor

import (
	"context"
	"errors"

	"github.com/lxc/lxd/lxd/fsmonitor/drivers"
	"github.com/lxc/lxd/lxd/storage/filesystem"
	"github.com/lxc/lxd/shared/logger"
)

// New creates a new FSMonitor instance.
func New(ctx context.Context, path string) (FSMonitor, error) {
	startMonitor := func(driverName string) (drivers.Driver, logger.Logger, error) {
		logger := logger.AddContext(logger.Log, logger.Ctx{"driver": driverName})

		driver, err := drivers.Load(ctx, logger, driverName, path)
		if err != nil {
			return nil, nil, err
		}

		return driver, logger, nil
	}

	if !filesystem.IsMountPoint(path) {
		return nil, errors.New("Path needs to be a mountpoint")
	}

	driver, monLogger, err := startMonitor("fanotify")
	if err != nil {
		logger.Warn("Failed to initialize fanotify, falling back on inotify", logger.Ctx{"err": err})
		driver, monLogger, err = startMonitor("inotify")
		if err != nil {
			return nil, err
		}
	}

	logger.Info("Initialized filesystem monitor", logger.Ctx{"path": path, "driver": driver.Name()})

	monitor := fsMonitor{
		driver: driver,
		logger: monLogger,
	}

	return &monitor, nil
}
