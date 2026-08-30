package shared

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/logger"
)

// SnapSetHealth calls snapctl set-health. No-op when not running inside the snap.
// If ctx has no deadline, a 5s timeout is applied.
func SnapSetHealth(ctx context.Context, status string, message string) {
	if !InSnap() {
		return
	}

	_, ok := ctx.Deadline()
	if !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	args := []string{"set-health", status}
	if message != "" {
		args = append(args, message)
	}

	_, err := RunCommand(ctx, "snapctl", args...)
	if err != nil {
		logger.Warn("Failed setting snap health", logger.Ctx{"status": status, "message": message, "err": err})
	}
}
