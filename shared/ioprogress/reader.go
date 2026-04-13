package ioprogress

import (
	"errors"
	"io"
)

// ProgressReader is a wrapper around ReadCloser which allows for progress tracking.
type ProgressReader struct {
	io.ReadCloser
	Tracker *ProgressTracker
}

// Read in [ProgressReader] is the same as io.Read.
func (pt *ProgressReader) Read(p []byte) (int, error) {
	if pt.ReadCloser == nil {
		return 0, errors.New("ProgressReader is missing a reader")
	}

	// Do normal reader tasks
	n, err := pt.ReadCloser.Read(p)

	// Do the actual progress tracking
	if pt.Tracker != nil {
		pt.Tracker.update(n)
	}

	return n, err
}
