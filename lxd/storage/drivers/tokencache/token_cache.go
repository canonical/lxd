package tokencache

import (
	"context"
	"fmt"
	"sync"

	"github.com/canonical/lxd/lxd/locking"
)

// TokenCache is a concurrent, per-key synchronized cache for pointer values of
// type T.
//
// It allows fast, lock free value retrieval while ensuing only one replace
// function will run at a time for a given key.
type TokenCache[T any] struct {
	// name of the token cache, used as part of the global lock name
	name string

	// concurrent map that holds *T items
	items sync.Map
}

// New creates a new TokenCache with the provided name.
func New[T any](name string) *TokenCache[T] {
	return &TokenCache[T]{name: name}
}

// lock acquires a lock for the provided key.
func (tc *TokenCache[T]) lock(key string) (locking.UnlockFunc, error) {
	lockName := fmt.Sprintf("storage/tokenCache/%s/%s", tc.name, key)
	return locking.Lock(context.Background(), lockName)
}

// Load retrieves value associated with the provided key.
func (tc *TokenCache[T]) Load(key string) *T {
	value, has := tc.items.Load(key)
	if !has {
		return nil
	}

	return value.(*T) //nolint:revive // No need to assert types as it is guaranteed that all values are of type *T.
}

// Replace perform concurrency safe replacement of a value associated with
// the provided key. It is guaranteed that when concurrent replace operations
// are running only one replace function will run at a time for a given key.
//
// If replace function returns non nil error value, it indicates that
// the replacing operation has failed and no value should be modified.
func (tc *TokenCache[T]) Replace(key string, replaceFunc func(*T) (*T, error)) (*T, error) {
	unlock, err := tc.lock(key)
	if err != nil {
		return nil, err
	}

	defer unlock()

	value, err := replaceFunc(tc.Load(key))
	if err != nil {
		// Replacement failed.
		return value, err
	}

	if value == nil {
		// Value should be removed.
		tc.items.Delete(key)
		return value, nil
	}

	// Value should be replaced.
	tc.items.Store(key, value)
	return value, nil
}

// Range calls yield sequentially for each key, value pair in the map. If yield
// returns false, range stops the iteration. It behaves similarly to Map.Range
// from the "sync" package.
func (tc *TokenCache[T]) Range(yield func(key string, value *T) bool) {
	for key, value := range tc.items.Range {
		if !yield(key.(string), value.(*T)) {
			return
		}
	}
}
