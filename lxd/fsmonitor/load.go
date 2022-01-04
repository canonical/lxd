package fsmonitor

import (
	"context"
	"errors"

	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/lxc/lxd/lxd/fsmonitor/drivers"
	"github.com/lxc/lxd/lxd/storage/filesystem"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
)

// New creates a new FSMonitor instance.
func New(ctx context.Context, path string) (FSMonitor, error) {
	startMonitor := func(driverName string) (drivers.Driver, logger.Logger, error) {
		logger := logging.AddContext(logger.Log, log.Ctx{"driver": driverName})

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
		logger.Warn("Failed to initialize fanotify, falling back on fsnotify", log.Ctx{"err": err})
		driver, monLogger, err = startMonitor("fsnotify")
		if err != nil {
			return nil, err
		}
	}

	logger.Debug("Initialized filesystem monitor", log.Ctx{"path": path})

	monitor := fsMonitor{
		driver: driver,
		logger: monLogger,
	}

	return &monitor, nil
}
