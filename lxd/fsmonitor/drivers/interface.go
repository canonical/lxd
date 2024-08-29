package drivers

import (
	"context"

	"github.com/canonical/lxd/lxd/fsmonitor"
	"github.com/canonical/lxd/shared/logger"
)

// driver is the extended internal interface.
type driver interface {
	fsmonitor.FSMonitor

	init(logger logger.Logger, path string)
	load(ctx context.Context) error
}
