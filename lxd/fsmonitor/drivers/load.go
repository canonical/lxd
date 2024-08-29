package drivers

import (
	"context"
	"errors"
	"fmt"

	"github.com/canonical/lxd/lxd/fsmonitor"
	"github.com/canonical/lxd/lxd/storage/filesystem"
	"github.com/canonical/lxd/shared/logger"
)

var drivers = map[string]func() driver{
	"inotify":  func() driver { return &inotify{} },
	"fanotify": func() driver { return &fanotify{} },
}

// Load returns a new fsmonitor.FSMonitor with an applicable Driver.
func Load(ctx context.Context, path string) (fsmonitor.FSMonitor, error) {
	startMonitor := func(driverName string) (Driver, error) {
		logger := logger.AddContext(logger.Ctx{"driver": driverName})

		df, ok := drivers[driverName]
		if !ok {
			return nil, ErrUnknownDriver
		}

		d := df()

		d.init(logger, path)

		err := d.load(ctx)
		if err != nil {
			return nil, fmt.Errorf("Failed to load fsmonitor driver %q: %w", driverName, err)
		}

		return d, nil
	}

	if !filesystem.IsMountPoint(path) {
		return nil, errors.New("Path needs to be a mountpoint")
	}

	driver, err := startMonitor("fanotify")
	if err != nil {
		logger.Warn("Failed to initialize fanotify, falling back on inotify", logger.Ctx{"err": err})
		driver, err = startMonitor("inotify")
		if err != nil {
			return nil, err
		}
	}

	logger.Info("Initialized filesystem monitor", logger.Ctx{"path": path, "driver": driver.Name()})

	return driver, nil
}
