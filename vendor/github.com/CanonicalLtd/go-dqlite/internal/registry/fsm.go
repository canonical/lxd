package registry

// Index returns the last Raft log index that was successfully applied the FSM.
func (r *Registry) Index() uint64 {
	return r.index
}

// IndexUpdate updates the index of the last log applied by the FSM we're
// associated with.
func (r *Registry) IndexUpdate(index uint64) {
	r.index = index
}

// Frames returns the number of frames that have been written to the WAL so
// far.
func (r *Registry) Frames() uint64 {
	return r.frames
}

// FramesIncrease increases by the given amount the number of frames written to
// the WAL so far.
func (r *Registry) FramesIncrease(n uint64) {
	r.frames += n
}

// FramesReset resets the WAL frames counter to zero.
func (r *Registry) FramesReset() {
	r.frames = 0
}
