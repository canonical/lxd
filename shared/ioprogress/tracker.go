package ioprogress

import (
	"strconv"
	"strings"
	"time"

	"github.com/canonical/lxd/shared/units"
)

// ProgressTracker provides the stream information needed for tracking.
type ProgressTracker struct {
	Length  int64
	Handler func(percentage int64, bytesTransferred int64, bytesPerSecond int64)

	percentage float64
	total      int64
	start      *time.Time
	last       *time.Time
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
	var percentage float64
	if pt.Length > 0 {
		// If running in relative mode, check that we increased by at least 1%
		percentage = float64(pt.total) / float64(pt.Length) * float64(100)
		if percentage-pt.percentage < 0.9 {
			return
		}
	} else {
		// If running in absolute mode, check that at least a second elapsed
		interval := time.Since(*pt.last).Seconds()
		if interval < 1 {
			return
		}
	}

	// Determine speed
	speedInt := int64(0)
	duration := time.Since(*pt.start).Seconds()
	if duration > 0 {
		speed := float64(pt.total) / duration
		speedInt = int64(speed)
	}

	// Determine progress
	if pt.Length > 0 {
		pt.percentage = percentage
	} else {
		// Update timestamp
		cur := time.Now()
		pt.last = &cur
	}

	pt.Handler(min(int64(pt.percentage)+1, 100), pt.total, speedInt)
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
