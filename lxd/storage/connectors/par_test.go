package connectors

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	// Number of concurrent operations.
	concurrency = 20

	// Number of attempts to trigger a racing write by each concurrent operation.
	attempts = 100
)

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func testRaceParFunc(t *testing.T, parFn func(context.Context, parOp[string], ...string) (<-chan struct{}, <-chan error)) {
	// Ensure via race detection that no operation is running after any channel
	// returned by par is closed.

	// startWrite controls when to start concurrent write to out.
	startWrite := make(chan struct{})
	out := "original"

	op := func(_ context.Context, arg string) error {
		t.Logf("operation %q started", arg)
		defer t.Logf("operation %q terminated", arg)
		for range attempts {
			select {
			case <-startWrite:
				// Potential concurrent modification of out. Only possible if the operation
				// is running while startWrite was already closed.
				out = arg
			default:
			}

			runtime.Gosched()
		}

		return nil
	}

	args := make([]string, concurrency)
	for i := range args {
		args[i] = strconv.Itoa(i)
	}

	doneCh, errsCh := parFn(t.Context(), op, args...)
	assert.NotNil(t, doneCh, "done channel is nil")
	assert.Equal(t, concurrency, cap(errsCh), "bad errors channel capacity")

	errsSlc := collectChan(errsCh)
	close(startWrite)
	assert.True(t, isClosed(doneCh), "par reported not done after closing errors channel")

	assert.Equal(t, make([]error, concurrency), errsSlc, "Non nil error returned")
	assert.Equal(t, "original", out, "invalid concurrent write detected")
}

func TestRacePar(t *testing.T) {
	testRaceParFunc(t, par)
}

func TestRaceParWithMode(t *testing.T) {
	for i := range concurrency + 1 {
		t.Run(fmt.Sprintf("parMode(%d)", i), func(t *testing.T) {
			testRaceParFunc(t, func(ctx context.Context, op parOp[string], args ...string) (<-chan struct{}, <-chan error) {
				success, done, errs := parWithMode(ctx, parMode(i), op, args...)
				assert.Equal(t, i <= concurrency, success)
				assert.LessOrEqual(t, i, len(errs))
				return done, errs
			})
		})
	}
}
