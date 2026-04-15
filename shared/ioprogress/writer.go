package ioprogress

import (
	"errors"
	"io"
)

// WriterWrapper is a function that returns a [ProgressWriter] from a given [io.WriteCloser] using a previously
// configured [ProgressWriter].
type WriterWrapper func(closer io.WriteCloser) io.WriteCloser

// NewProgressWriter returns a [ProgressWriter] with the given list of [TrackerOption].
func NewProgressWriter(writeCloser io.WriteCloser, trackerOpts ...TrackerOption) io.WriteCloser {
	tracker := NewProgressTracker(trackerOpts...)

	// If the caller did not set a handler via any options then there is nothing to report to, so avoid the wrapper
	// and have the caller write directly to the input writer.
	if tracker.Handler == nil {
		return writeCloser
	}

	return &ProgressWriter{
		WriteCloser: writeCloser,
		Tracker:     tracker,
	}
}

// NewProgressWriterWrapper returns a [WriterWrapper] with the given list of [TrackerOption].
func NewProgressWriterWrapper(trackerOpts ...TrackerOption) func(io.WriteCloser) io.WriteCloser {
	tracker := NewProgressTracker(trackerOpts...)
	return func(writeCloser io.WriteCloser) io.WriteCloser {
		// If the caller did not set a handler via any options then there is nothing to report to, so avoid the wrapper
		// and have the caller write directly to the input writer.
		if tracker.Handler == nil {
			return writeCloser
		}

		return &ProgressWriter{
			WriteCloser: writeCloser,
			Tracker:     tracker,
		}
	}
}

// ProgressWriter is a wrapper around an [io.WriteCloser] which allows for progress tracking.
type ProgressWriter struct {
	io.WriteCloser
	Tracker *ProgressTracker
}

// Write in [ProgressWriter] is the same as io.Write.
func (pt *ProgressWriter) Write(p []byte) (int, error) {
	// Do normal writer tasks
	if pt.WriteCloser == nil {
		return 0, errors.New("ProgressWriter is missing a writer")
	}

	n, err := pt.WriteCloser.Write(p)

	// Do the actual progress tracking
	if pt.Tracker != nil {
		pt.Tracker.update(n)
	}

	return n, err
}
