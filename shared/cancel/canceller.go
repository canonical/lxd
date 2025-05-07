package cancel

import (
	"context"
	"sync"
	"time"
)

// Canceller extends context.Context with a built-in Cancel function.
type Canceller interface {
	context.Context
	Cancel()
}

// canceller is an implementation of Canceller that behaves similarly to context.WithCancel()
// but does not use go routines, which makes it more convenient to store in a long-lived struct.
// It implements the context.Context interface.
type canceller struct {
	ch   chan struct{}
	once func()
}

// Err returns nil if Cancel() has not been called yet.
// If Cancel() has been called then returns context.Canceled.
// After Err returns a non-nil error, successive calls to Err return the same error.
func (c *canceller) Err() error {
	select {
	case <-c.ch:
		return context.Canceled
	default:
		return nil
	}
}

// Cancel the Canceller.
// Can be called multiple times safely.
func (c *canceller) Cancel() {
	c.once()
}

// Done returns a channel that's closed when the canceller.Cancel() is called.
// Successive calls to Done return the same value.
func (c *canceller) Done() <-chan struct{} {
	return c.ch
}

// Value is not implemented.
func (c *canceller) Value(key any) any {
	return nil
}

// Deadline is not implemented.
func (c *canceller) Deadline() (deadline time.Time, ok bool) {
	return time.Time{}, false
}

// New returns a new Canceller.
func New() Canceller {
	ch := make(chan struct{})

	return &canceller{
		ch: ch,
		once: sync.OnceFunc(func() {
			close(ch)
		}),
	}
}
