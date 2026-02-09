package drivers

import "sync"

// keyedMutex represents per-key lock (keyed mutex). It provides mutual exclusion locks scoped to arbitrary strings keys.
//
// Each distinct key behaves like Mutex from the "sync" package - subsequent calls to Lock with the given key will block until Unlock with the same key is called. Calls with different keys will proceed concurrently.
//
// Internally, it automatically allocates and deallocates mutexes as needed, keeping memory usage bounded to the number of concurrently hold locks.
type keyedMutex struct {
	mtx   sync.Mutex
	locks map[string]*keyedMutexItem
}

type keyedMutexItem struct {
	mtx  sync.Mutex
	refs int
}

// inc increments the reference counter associated with the given key, allocating resources, if needed. It returns item associated with the key.
func (km *keyedMutex) inc(key string) *keyedMutexItem {
	km.mtx.Lock()
	defer km.mtx.Unlock()
	if km.locks == nil {
		km.locks = map[string]*keyedMutexItem{}
	}

	i, ok := km.locks[key]
	if !ok {
		i = &keyedMutexItem{}
		km.locks[key] = i
	}

	i.refs++
	return i
}

// dec decrement the reference counter associated with the given key, releasing resources, if needed. It returns item associated with the key, or nil if key is unknown.
func (km *keyedMutex) dec(key string) *keyedMutexItem {
	km.mtx.Lock()
	defer km.mtx.Unlock()
	if km.locks == nil {
		return nil
	}

	i, ok := km.locks[key]
	if !ok {
		return nil
	}

	i.refs--
	if i.refs <= 0 {
		delete(km.locks, key)
	}

	return i
}

// Lock acquires an exclusive lock associated with the provided key.
func (km *keyedMutex) Lock(key string) {
	i := km.inc(key)
	i.mtx.Lock()
}

// Unlock releases an exclusive lock associated with the provided key.
func (km *keyedMutex) Unlock(key string) {
	i := km.dec(key)
	if i != nil {
		i.mtx.Unlock()
	}
}

// tokenCache is a concurrent, per-key synchronized cache for pointer values of type T.
//
// It allows fast, lock free value retrieval while ensuing only one replace function will run at a time for a given key.
type tokenCache[T any] struct {
	mtx   keyedMutex // concurrent per-key mutex; locked when modification of a given key is in progress
	items sync.Map   // concurrent map that holds *T items
}

// Load retrieves value associated with the provided key.
func (tc *tokenCache[T]) Load(key string) *T {
	value, has := tc.items.Load(key)
	if !has {
		return nil
	}

	return value.(*T) //nolint:revive // No need to assert types as it is guaranteed that all values are of type *T.
}

// Replace perform concurrency safe replacement of a value associated with the provided key. It is guaranteed that when concurrent replace operations are running only one replace function will run at a time for a given key.
//
// If replace function returns non nil error value, it indicates that the replacing operation has failed and no value should be modified.
func (tc *tokenCache[T]) Replace(key string, replaceFunc func(*T) (*T, error)) (*T, error) {
	tc.mtx.Lock(key)
	defer tc.mtx.Unlock(key)

	value, err := replaceFunc(tc.Load(key))
	if err != nil { // replacement failed
		return value, err
	}

	if value == nil { // value should be removed
		tc.items.Delete(key)
		return value, nil
	}
	// value should be replaced
	tc.items.Store(key, value)
	return value, nil
}

// Range calls yield sequentially for each key, value pair in the map. If yield returns false, range stops the iteration. It behaves similarly to Map.Range from the "sync" package.
func (tc *tokenCache[T]) Range(yield func(key string, value *T) bool) {
	for key, value := range tc.items.Range {
		if !yield(key.(string), value.(*T)) {
			return
		}
	}
}
