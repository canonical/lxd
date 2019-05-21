package bindings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AssertNoMemoryLeaks is a test helper asserting that current allocation count
// and used memory are both zero.
func AssertNoMemoryLeaks(t *testing.T) {
	t.Helper()

	current, _, err := StatusMallocCount(true)
	require.NoError(t, err)

	assert.Equal(t, 0, current, "malloc count leak")

	current, _, err = StatusMemoryUsed(true)
	require.NoError(t, err)

	assert.Equal(t, 0, current, "memory leak")
}
