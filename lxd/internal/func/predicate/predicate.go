// Package predicate provides helpers for composing boolean predicate functions.
package predicate

// And returns a predicate reporting true only if every one of preds reports true for the same
// argument. preds are evaluated left to right with short-circuiting: once one returns false, the
// remaining predicates are never called. And() with no predicates always reports true.
func And[T any](preds ...func(T) bool) func(T) bool {
	return func(v T) bool {
		for _, p := range preds {
			if !p(v) {
				return false
			}
		}

		return true
	}
}

// Or returns a predicate reporting true if any one of preds reports true for the same argument.
// preds are evaluated left to right with short-circuiting: once one returns true, the remaining
// predicates are never called. Or() with no predicates always reports false.
func Or[T any](preds ...func(T) bool) func(T) bool {
	return func(v T) bool {
		for _, p := range preds {
			if p(v) {
				return true
			}
		}

		return false
	}
}

// Not returns a predicate reporting the logical negation of pred's result.
func Not[T any](pred func(T) bool) func(T) bool {
	return func(v T) bool {
		return !pred(v)
	}
}
