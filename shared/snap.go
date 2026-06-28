package shared

import (
	"context"
	"time"

	"github.com/canonical/lxd/shared/logger"
)

// SnapSetHealth calls snapctl set-health. No-op when not running inside the snap.
func SnapSetHealth(status string, message string) {
	if !InSnap() {
		return
	}

	args := []string{"set-health", status}
	if message != "" {
		args = append(args, message)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := RunCommand(ctx, "snapctl", args...)
	if err != nil {
		logger.Warn("Failed setting snap health", logger.Ctx{"status": status, "message": message, "err": err})
	}
}
