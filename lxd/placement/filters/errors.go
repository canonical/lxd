package filters

// NoCandidatesError is returned by a FilterStage when it legitimately leaves no candidate cluster
// member, as opposed to failing for an unrelated reason such as a database error.
type NoCandidatesError struct {
	// Func is the name of the function that ran out of candidates.
	Func string

	// Reason describes why no candidates remain.
	Reason string
}

// Error renders the failing function and its reason.
func (e *NoCandidatesError) Error() string {
	return e.Func + ": " + e.Reason
}
