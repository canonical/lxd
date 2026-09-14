// Package sets provides a small, generic set data structure shared across the codebase.
package sets

import (
	"iter"
	"maps"
	"slices"
)

// Set is an unordered collection of unique, comparable elements — the map-backed set idiom
// (map[T]struct{}) already used ad hoc throughout this codebase, given a name and a small set of
// methods so callers stop hand-rolling membership checks, unions, and iteration each time.
//
// The zero value is not usable for writes — a nil Set reads exactly like an empty one (Contains,
// Len, All, Slice, Equal, and IsSubsetOf all treat it as empty, the same way a nil map does), but
// Add panics on it, exactly like assigning into a nil map. Use New or FromSeq to construct one.
type Set[T comparable] map[T]struct{}

// New returns a Set containing the given items, deduplicated.
func New[T comparable](items ...T) Set[T] {
	return FromSeq(slices.Values(items))
}

// FromSeq returns a Set containing every element seq yields, deduplicated.
func FromSeq[T comparable](seq iter.Seq[T]) Set[T] {
	s := make(Set[T])
	for v := range seq {
		s[v] = struct{}{}
	}

	return s
}

// Add inserts the given items into s.
func (s Set[T]) Add(items ...T) {
	for _, v := range items {
		s[v] = struct{}{}
	}
}

// Remove deletes the given items from s. Removing an item not in s is a no-op.
func (s Set[T]) Remove(items ...T) {
	for _, v := range items {
		delete(s, v)
	}
}

// Contains reports whether item is in s.
func (s Set[T]) Contains(item T) bool {
	_, ok := s[item]
	return ok
}

// Len returns the number of elements in s.
func (s Set[T]) Len() int {
	return len(s)
}

// All returns an iterator over every element of s, in no particular order.
func (s Set[T]) All() iter.Seq[T] {
	return maps.Keys(s)
}

// Slice returns the elements of s as a slice, in no particular order.
func (s Set[T]) Slice() []T {
	return slices.Collect(s.All())
}

// Equal reports whether s and other contain exactly the same elements.
func (s Set[T]) Equal(other Set[T]) bool {
	return maps.Equal(s, other)
}

// IsSubsetOf reports whether every element of s is also in other.
func (s Set[T]) IsSubsetOf(other Set[T]) bool {
	for v := range s {
		if !other.Contains(v) {
			return false
		}
	}

	return true
}

// Union returns a new Set containing every element in s or other (or both). Neither input is
// modified.
func (s Set[T]) Union(other Set[T]) Set[T] {
	result := make(Set[T], len(s)+len(other))
	for v := range s {
		result[v] = struct{}{}
	}

	for v := range other {
		result[v] = struct{}{}
	}

	return result
}

// Intersect returns a new Set containing only the elements present in both s and other. Neither
// input is modified.
func (s Set[T]) Intersect(other Set[T]) Set[T] {
	small, large := s, other
	if len(other) < len(s) {
		small, large = other, s
	}

	result := make(Set[T])
	for v := range small {
		if large.Contains(v) {
			result[v] = struct{}{}
		}
	}

	return result
}

// Difference returns a new Set containing the elements of s that are not in other. Neither input
// is modified.
func (s Set[T]) Difference(other Set[T]) Set[T] {
	result := make(Set[T])
	for v := range s {
		if !other.Contains(v) {
			result[v] = struct{}{}
		}
	}

	return result
}
