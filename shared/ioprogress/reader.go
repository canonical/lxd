package ioprogress

import (
	"io"
)

type ProgressReader struct {
	io.ReadCloser
	Tracker *ProgressTracker
}

func (pt *ProgressReader) Read(p []byte) (int, error) {
	// Do normal reader tasks
	n, err := pt.ReadCloser.Read(p)

	// Do the actual progress tracking
	if pt.Tracker != nil {
		pt.Tracker.total += int64(n)
		pt.Tracker.Update(n)
	}

	return n, err
}
