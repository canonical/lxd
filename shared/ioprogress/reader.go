package ioprogress

import (
	"errors"
	"io"
)

// ReaderWrapper is a function that returns a [ProgressReader] from a given [io.ReadCloser] using a previously
// configured [ProgressReader].
type ReaderWrapper func(closer io.ReadCloser) io.ReadCloser

// NewProgressReader returns a [ProgressReader] with the given list of [TrackerOption].
func NewProgressReader(readCloser io.ReadCloser, trackerOpts ...TrackerOption) io.ReadCloser {
	tracker := NewProgressTracker(trackerOpts...)

	// If the caller did not set a handler via any options then there is nothing to report to, so avoid the wrapper
	// and have the caller read directly from the input reader.
	if tracker.Handler == nil {
		return readCloser
	}

	return &ProgressReader{
		ReadCloser: readCloser,
		Tracker:    tracker,
	}
}

// NewProgressReaderWrapper returns a [ReaderWrapper] with the given list of [TrackerOption].
func NewProgressReaderWrapper(trackerOpts ...TrackerOption) ReaderWrapper {
	tracker := NewProgressTracker(trackerOpts...)

	return func(readCloser io.ReadCloser) io.ReadCloser {
		// If the caller did not set a handler via any options then there is nothing to report to, so avoid the wrapper
		// and have the caller read directly from the input reader.
		if tracker.Handler == nil {
			return readCloser
		}

		return &ProgressReader{
			ReadCloser: readCloser,
			Tracker:    tracker,
		}
	}
}

// ProgressReader is a wrapper around [io.ReadCloser] which allows for progress tracking.
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
