package cancel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/canonical/lxd/shared/cancel"
)

func TestCancel(t *testing.T) {
	c := cancel.New()

	// Test Err returns nil before cancellation.
	require.NoError(t, c.Err())

	// Test c.Done() returns an unclosed channel.
	isClosed := false
	select {
	case <-c.Done():
		isClosed = true
	default:
	}

	require.False(t, isClosed)

	// Cancel the Canceller.
	c.Cancel()

	// Test successive calls to Err().
	require.ErrorIs(t, c.Err(), context.Canceled)
	require.ErrorIs(t, c.Err(), context.Canceled)

	// Test c.Done() returns a closed channel.
	isClosed = false
	select {
	case <-c.Done():
		isClosed = true
	default:
	}

	require.True(t, isClosed)

	// Check Cancel can be called multiple times.
	c.Cancel()
}
