package revert

// Reverter is a helper type to manage revert functions.
type Reverter struct {
	revertFuncs []func()
}

// New returns a new Reverter.
func New() *Reverter {
	return &Reverter{}
}

// Add adds a revert function to the list to be run when Revert() is called.
func (r *Reverter) Add(f func()) {
	r.revertFuncs = append(r.revertFuncs, f)
}

// Fail runs any revert functions in the reverse order they were added.
// Should be used with defer or when a task has encountered an error and needs to be reverted.
func (r *Reverter) Fail() {
	funcCount := len(r.revertFuncs)
	for k := range r.revertFuncs {
		// Run the revert functions in reverse order.
		k = funcCount - 1 - k
		r.revertFuncs[k]()
	}
}

// Success clears the revert functions previously added.
// Should be called on successful completion of a task to prevent revert functions from being run.
func (r *Reverter) Success() {
	r.revertFuncs = nil
}
