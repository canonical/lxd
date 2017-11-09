package debug

import (
	"sync"

	"golang.org/x/net/context"
)

// Start the given LXD daemon debug activities.
//
// Return a function that can be used to stop all debug activities that were
// started, along with an error if any activity could not be started.
func Start(activities ...Activity) (func(), error) {
	// First create the debug activities functions that will be run in
	// individual goroutines. If any error happens here we bail out before
	// actually spawning any activity.
	functions := make([]activityFunc, len(activities))
	for i, activity := range activities {
		f, err := activity()
		if err != nil {
			return func() {}, err
		}
		functions[i] = f
	}

	// Now spawn the debug activities.
	ctx, cancel := context.WithCancel(context.Background())
	wg := sync.WaitGroup{}
	for i := range functions {
		f := functions[i]
		if f == nil {
			// There's actually nothing to execute.
			continue
		}
		wg.Add(1)
		go func() {
			f(ctx)
			wg.Done()
		}()
	}

	stop := func() {
		cancel()
		wg.Wait()
	}
	return stop, nil
}

// Activity creates a specific debug activity function, returning an error if it
// can't be created for some reason.
type Activity func() (activityFunc, error)

// A function that executes a specific debug activity.
//
// It must terminate gracefully whenever the given context is done.
type activityFunc func(context.Context)
