// Package iterutil provides small generic helpers for working with iter.Seq sequences.
package iterutil

import (
	"iter"
)

// Filter returns a sequence containing only the elements of seq for which keep returns true.
func Filter[T any](seq iter.Seq[T], keep func(T) bool) iter.Seq[T] {
	return func(yield func(T) bool) {
		for v := range seq {
			if keep(v) && !yield(v) {
				return
			}
		}
	}
}
