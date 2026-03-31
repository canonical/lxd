package connectors

import (
	"context"
	"sync"
)

// parOp represents operation function executable by par and parWithMode
// functions.
type parOp[Arg any] = func(ctx context.Context, arg Arg) error

// parMode is a parameter dictating how functions should wait for concurrent
// operations (how many successfully completed operations are expected).
type parMode int

// duplicateChan duplicates values from the provided channel into two returned
// channels.
func duplicateChan[T any](in <-chan T) (<-chan T, <-chan T) { //nolint:revive // This unnamed results of the same type is not confusing - the function is duplicating channel creating two output channels from one input channel.
	cloneRoutine := func(in <-chan T, outA, outB chan<- T) {
		for v := range in {
			outA <- v
			outB <- v
		}

		close(outA)
		close(outB)
	}

	outA := make(chan T, cap(in))
	outB := make(chan T, cap(in))
	go cloneRoutine(in, outA, outB)
	return outA, outB
}

// collectChan collect all values from the provided channel into a slice.
func collectChan[T any](in <-chan T) []T {
	s := make([]T, 0, cap(in))
	for v := range in {
		s = append(s, v)
	}

	return s
}

// par executes the provided operation concurrently for each argument without
// waiting for results.
func par[Arg any](ctx context.Context, operation parOp[Arg], args ...Arg) (<-chan struct{}, <-chan error) {
	errs := make(chan error, len(args))
	done := make(chan struct{})

	// No arguments provided - nothing to do.
	if len(args) == 0 {
		close(errs)
		close(done)
		return done, errs
	}

	// Start goroutine per argument.
	operationRoutine := func(ctx context.Context, wg *sync.WaitGroup, arg Arg, errs chan<- error) {
		defer wg.Done()

		// Send regardless if it is a nil or error to signify operation end.
		errs <- operation(ctx, arg)
	}

	// Close output channels once all operation goroutines are done.
	managementRoutine := func(wg *sync.WaitGroup, cancel context.CancelFunc, done chan<- struct{}, errs chan<- error) {
		wg.Wait()
		cancel()
		close(errs)
		close(done)
	}

	ctx, cancel := context.WithCancel(ctx)
	wg := &sync.WaitGroup{}

	wg.Add(len(args))
	for _, arg := range args {
		go operationRoutine(ctx, wg, arg, errs)
	}

	go managementRoutine(wg, cancel, done, errs)

	return done, errs
}

// parWithMode executes the provided operation concurrently for each argument
// and waits for results according to the provided mode.
func parWithMode[Arg any](ctx context.Context, mode parMode, operation parOp[Arg], args ...Arg) (bool, <-chan struct{}, <-chan error) {
	// Zero mode means caller do not wants to wait for any operations.
	if mode == 0 {
		done, errs := par(ctx, operation, args...)
		return true, done, errs
	}

	modeApplyingRoutine := func(mode parMode, cancel context.CancelFunc, success chan<- bool, done <-chan struct{}, errs <-chan error) {
		// Always cleanup at the end.
		defer func() {
			// Return to caller and cleanup in the background.
			close(success)

			// Wait till all operations end.
			<-done

			// Cancel parent context, if not already cancelled.
			cancel()
		}()

		total := cap(errs)
		successes := 0
		failures := 0

		for err := range errs {
			if err != nil {
				failures++
			} else {
				successes++
			}

			if successes >= int(mode) {
				// Expected limit of successfully completed operations reached.
				success <- true
				return
			}

			if total-failures < int(mode) {
				// To many operations failed - reaching the limit of successfully completed
				// operations is impossible. Cancel the rest of operations and return.
				cancel()
				return
			}
		}

		// Mode value is too high - limit of successfully completed operations was not
		// reached. Cancel parent context and return.
		cancel()
	}

	ctx, cancel := context.WithCancel(ctx)
	done, errs := par(ctx, operation, args...)

	success := make(chan bool, 1)
	errs, errsStats := duplicateChan(errs)

	go modeApplyingRoutine(mode, cancel, success, done, errsStats)

	return <-success, done, errs
}
