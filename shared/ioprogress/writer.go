package ioprogress

import (
	"io"
)

type ProgressWriter struct {
	io.WriteCloser
	Tracker *ProgressTracker
}

func (pt *ProgressWriter) Write(p []byte) (int, error) {
	// Do normal writer tasks
	n, err := pt.WriteCloser.Write(p)

	// Do the actual progress tracking
	if pt.Tracker != nil {
		pt.Tracker.total += int64(n)
		pt.Tracker.Update(n)
	}

	return n, err
}
