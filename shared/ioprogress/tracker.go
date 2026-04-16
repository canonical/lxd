package ioprogress

import (
	"strconv"
	"strings"
	"time"

	"github.com/canonical/lxd/shared/units"
)

// TrackerOption is a function that configures a [ProgressTracker].
type TrackerOption func(tracker *ProgressTracker)

// ProgressReporter is a type which, given some action, can return a [ProgressHandler].
// This is useful when several different I/O actions are performed in the same API call or code path.
type ProgressReporter interface {
	ProgressHandler(action string) ProgressHandler
}

// ProgressUpdater is a type with a function defining what to do with [ProgressData].
type ProgressUpdater interface {
	UpdateProgress(data ProgressData)
}

// ProgressHandler is a function that receives [ProgressData] and performs some action with it (e.g. printing to stdout).
type ProgressHandler func(ProgressData)

// ProgressTracker provides the stream information needed for tracking.
type ProgressTracker struct {
	Length  int64
	Handler func(percentage int64, bytesTransferred int64, bytesPerSecond int64)

	percentage float64
	total      int64
	start      *time.Time
	last       *time.Time
}

// NewProgressTracker returns a [ProgressTracker] configured with the given list of [TrackerOption].
func NewProgressTracker(opts ...TrackerOption) *ProgressTracker {
	tracker := &ProgressTracker{}
	for _, option := range opts {
		option(tracker)
	}

	return tracker
}

// WithProgressReporter is a convenience for configuring a [ProgressTracker] for types that implement
// [ProgressReporter].
func WithProgressReporter(action string, reporter ProgressReporter) TrackerOption {
	return func(tracker *ProgressTracker) {
		if reporter == nil {
			return
		}

		(*ProgressTracker).withDescriptiveProgressHandler(tracker, "", reporter.ProgressHandler(action))
	}
}

// WithDescriptiveProgressReporter is a convenience for configuring a [ProgressTracker] for types that implement
// [ProgressReporter]. The description is prepended to [ProgressData.Text] with a ": " separator.
func WithDescriptiveProgressReporter(action string, description string, reporter ProgressReporter) TrackerOption {
	return func(tracker *ProgressTracker) {
		if reporter == nil {
			return
		}

		(*ProgressTracker).withDescriptiveProgressHandler(tracker, description, reporter.ProgressHandler(action))
	}
}

// WithProgressUpdater is a convenience for configuring a [ProgressTracker] for types that implement [ProgressUpdater].
func WithProgressUpdater(updater ProgressUpdater) TrackerOption {
	return func(tracker *ProgressTracker) {
		if updater == nil {
			return
		}

		(*ProgressTracker).withDescriptiveProgressHandler(tracker, "", updater.UpdateProgress)
	}
}

// WithProgressHandler sets a [ProgressHandler] for tracker updates.
func WithProgressHandler(handler ProgressHandler) TrackerOption {
	return func(tracker *ProgressTracker) {
		(*ProgressTracker).withDescriptiveProgressHandler(tracker, "", handler)
	}
}

// WithDescriptiveProgressHandler sets a [ProgressHandler] for tracker updates.
// The description is prepended to [ProgressData.Text] with a ": " separator.
func WithDescriptiveProgressHandler(description string, handler func(data ProgressData)) TrackerOption {
	return func(tracker *ProgressTracker) {
		(*ProgressTracker).withDescriptiveProgressHandler(tracker, description, handler)
	}
}

// WithLength sets the expected total number of bytes to be read.
func WithLength(length int64) TrackerOption {
	return func(tracker *ProgressTracker) {
		tracker.Length = length
	}
}

func (pt *ProgressTracker) withDescriptiveProgressHandler(description string, handler ProgressHandler) {
	if handler == nil {
		return
	}

	pt.Handler = func(percentage int64, bytesTransferred int64, bytesPerSecond int64) {
		handler(pt.buildProgressData(description, percentage, bytesTransferred, bytesPerSecond))
	}
}

func (pt *ProgressTracker) update(n int) {
	pt.total += int64(n)

	// Skip the rest if no handler attached
	if pt.Handler == nil {
		return
	}

	// Initialize start time if needed
	if pt.start == nil {
		cur := time.Now()
		pt.start = &cur
		pt.last = pt.start
	}

	// Skip if no data to count
	if n <= 0 {
		return
	}

	// Update interval handling (this is to prevent the tracker hook from being called too frequently).
	var percentComplete int64
	if pt.Length > 0 {
		// If running in relative mode, check that we increased by at least 1%
		percentage := float64(pt.total) / float64(pt.Length) * float64(100)
		if percentage-pt.percentage < 0.9 {
			return
		}

		pt.percentage = percentage
		percentComplete = min(int64(pt.percentage)+1, 100)
	} else {
		// If running in absolute mode, check that at least a second elapsed
		interval := time.Since(*pt.last).Seconds()
		if interval < 1 {
			return
		}

		// Update timestamp
		cur := time.Now()
		pt.last = &cur
	}

	// Determine speed
	var bytesPerSecond int64
	duration := time.Since(*pt.start).Seconds()
	if duration > 0 {
		speed := float64(pt.total) / duration
		bytesPerSecond = int64(speed)
	}

	pt.Handler(percentComplete, pt.total, bytesPerSecond)
}

func (pt *ProgressTracker) buildProgressData(description string, percentage int64, bytesTransferred int64, bytesPerSecond int64) ProgressData {
	var b strings.Builder

	// Expected max size of string is:
	// - Length of description + 2 (for colon and space)
	// - Length of total byte transfer representation <= 8 (up to 3 digits, 2 decimals, 2 units).
	// - Length of bytes per second representation <= 13 (as above, plus 5 for space, brackets, and "/s")
	// Percentage not included in calculation since the total bytes representation is longer.
	b.Grow(len(description) + 23)

	// Write the description followed by a colon if given.
	if description != "" {
		b.WriteString(description)
		b.WriteString(": ")
	}

	// Write the progress as {x}% if percentage is given (only set when Length is set).
	// Otherwise, write the total number of transferred bytes in human-readable form.
	var noProgress bool
	if percentage > 0 {
		b.WriteString(strconv.FormatInt(percentage, 10))
		b.WriteString("%")
	} else if bytesTransferred > 0 {
		b.WriteString(units.GetByteSizeString(bytesTransferred, 2))
	} else {
		noProgress = true
	}

	// Write the bytes per second if given. Wrap with brackets if progress was written.
	if bytesPerSecond > 0 {
		if noProgress {
			b.WriteString(units.GetByteSizeString(bytesPerSecond, 2))
			b.WriteString("/s")
		} else {
			b.WriteString(" (")
			b.WriteString(units.GetByteSizeString(bytesPerSecond, 2))
			b.WriteString("/s)")
		}
	}

	return ProgressData{
		Text:             b.String(),
		Percentage:       int(percentage),
		TransferredBytes: bytesTransferred,
		TotalBytes:       pt.Length,
		BytesPerSecond:   bytesPerSecond,
	}
}
