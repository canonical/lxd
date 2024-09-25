package drivers

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/canonical/lxd/lxd/fsmonitor"
	"github.com/canonical/lxd/shared/logger"
)

var drivers = map[string]func() driver{
	fsmonitor.DriverNameINotify:  func() driver { return &inotify{} },
	fsmonitor.DriverNameFANotify: func() driver { return &fanotify{} },
}

// Load returns a new fsmonitor.FSMonitor with an applicable Driver.
func Load(ctx context.Context, path string, events ...fsmonitor.Event) (fsmonitor.FSMonitor, error) {
	if len(events) == 0 {
		return nil, errors.New("Event types must be specified")
	}

	startMonitor := func(driverName string) (fsmonitor.FSMonitor, error) {
		logger := logger.AddContext(logger.Ctx{"driver": driverName})

		df, ok := drivers[driverName]
		if !ok {
			return nil, ErrUnknownDriver
		}

		d := df()

		d.init(logger, path, events)

		err := d.load(ctx)
		if err != nil {
			return nil, fmt.Errorf("Failed to load fsmonitor driver %q: %w", driverName, err)
		}

		return d, nil
	}

	driverName := os.Getenv("LXD_FSMONITOR_DRIVER")
	if driverName != "" {
		return startMonitor(driverName)
	}

	driver, err := startMonitor(fsmonitor.DriverNameFANotify)
	if err != nil {
		logger.Warn("Failed to initialize fanotify, falling back on inotify", logger.Ctx{"err": err})
		driver, err = startMonitor(fsmonitor.DriverNameINotify)
		if err != nil {
			return nil, err
		}
	}

	logger.Info("Initialized filesystem monitor", logger.Ctx{"path": path, "driver": driver.DriverName()})

	return driver, nil
}
