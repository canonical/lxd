package ioprogress

// The ProgressData struct contains details about an I/O task.
type ProgressData struct {
	// Text is a string representation of progress.
	Text string

	// Percentage is the percentage progress. This is only set if TotalBytes is non-zero.
	Percentage int

	// TransferredBytes is the number of transferred bytes.
	TransferredBytes int64

	// TotalBytes is the number of expected bytes.
	TotalBytes int64

	// Bytes per second (mean value calculated since I/O operation started).
	BytesPerSecond int64
}
