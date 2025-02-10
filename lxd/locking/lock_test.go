package locking

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLockFriendly(t *testing.T) {
	tests := []struct {
		name              string
		err               error
		subsequentCallers int
	}{
		{
			name: "The first lock can always be obtained",
		},
		{
			name:              "Subsequent callers are unblocked accordingly",
			subsequentCallers: 10,
		},
	}

	for _, test := range tests {
		friendly, unlock, unlockFriendly, err := LockFriendly(context.TODO(), "test")
		if test.err != nil {
			assert.Equal(t, test.err.Error(), err.Error())
		}

		// The first lock can always be obtained and isn't "friendly".
		assert.Equal(t, false, friendly)

		// The unlock functions of the first lock are always not nil.
		assert.NotNil(t, unlock)
		assert.NotNil(t, unlockFriendly)

		if test.subsequentCallers > 0 {
			var wg sync.WaitGroup
			for i := 0; i < test.subsequentCallers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()

					friendly, unlock, unlockFriendly, err := LockFriendly(context.TODO(), "test")
					assert.Nil(t, err)

					// The lock was acquired friendly which means this subsequent caller can proceed.
					assert.True(t, friendly)

					// No unlock functions are returned as this is up to the preceding caller.
					assert.Nil(t, unlock)
					assert.Nil(t, unlockFriendly)
				}()
			}

			// Friendly unlock subsequent callers.
			unlockFriendly()

			// Wait for all the routines to return.
			wg.Wait()
		}

		// Unlock the first lock.
		unlock()
	}
}
