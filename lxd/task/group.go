package task

import (
	"sync"
	"time"

	"golang.org/x/net/context"
)

// Group of tasks sharing the same lifecycle.
//
// All tasks in a group will be started and stopped at the same time.
type Group struct {
	cancel func()
	wg     sync.WaitGroup
	tasks  []Task
}

// Add a new task to the group, returning its index.
func (g *Group) Add(f Func, schedule Schedule) *Task {
	i := len(g.tasks)
	g.tasks = append(g.tasks, Task{
		f:        f,
		schedule: schedule,
		reset:    make(chan struct{}, 16), // Buffered to not block senders
	})
	return &g.tasks[i]
}

// Start all the tasks in the group.
func (g *Group) Start() {
	ctx := context.Background()
	ctx, g.cancel = context.WithCancel(ctx)
	g.wg.Add(len(g.tasks))
	for i := range g.tasks {
		task := g.tasks[i] // Local variable for the closure below.
		go func() {
			task.loop(ctx)
			g.wg.Done()
		}()
	}
}

// Stop all tasks in the group.
//
// This works by sending a cancellation signal to all tasks of the
// group and waiting for them to terminate.
//
// If a task is idle (i.e. not executing its task function) it will terminate
// immediately.
//
// If a task is busy executing its task function, the cancellation signal will
// propagate through the context passed to it, and the task will block waiting
// for the function to terminate.
//
// In case the given timeout expires before all tasks complete, this method
// exits immediately and returns an error, otherwise it returns nil.
func (g *Group) Stop(timeout time.Duration) error {
	if g.cancel == nil {
		// We were not even started
		return nil
	}
	g.cancel()

	graceful := make(chan struct{}, 1)
	go func() {
		g.wg.Wait()
		close(graceful)
	}()

	// Wait for graceful termination, but abort if the context expires.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-graceful:
		return nil

	}
}
