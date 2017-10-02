package debug

import (
	"time"

	"github.com/lxc/lxd/lxd/task"
	"github.com/lxc/lxd/shared/logger"
	"golang.org/x/net/context"
)

// Goroutines starts a task to print the goroutines stack at the given interval.
func Goroutines(seconds int) Activity {
	return func() (activityFunc, error) {
		if seconds <= 0 {
			return nil, nil
		}

		schedule := task.Every(time.Duration(seconds) * time.Second)

		f := func(ctx context.Context) {
			stop, _ := task.Start(goroutinesTaskFunc, schedule)
			<-ctx.Done()

			// Stop the task, giving it some little time to finish
			// if it's in the middle of a print.
			stop(100 * time.Millisecond)
		}
		return f, nil
	}
}

func goroutinesTaskFunc(context.Context) {
	logger.Debugf(logger.GetStack())
}
