package utils

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/ioprogress"
)

// ProgressRenderer tracks the progress information
type ProgressRenderer struct {
	Format string

	maxLength int
	wait      time.Time
	done      bool
	lock      sync.Mutex
}

// Done prints the final status and prevents any update
func (p *ProgressRenderer) Done(msg string) {
	// Acquire rendering lock
	p.lock.Lock()
	defer p.lock.Unlock()

	// Check if we're already done
	if p.done {
		return
	}

	// Mark this renderer as done
	p.done = true

	// Print the new message
	if msg != "" {
		msg += "\n"
	}

	if len(msg) > p.maxLength {
		p.maxLength = len(msg)
	} else {
		fmt.Printf("\r%s", strings.Repeat(" ", p.maxLength))
	}

	fmt.Print("\r")
	fmt.Print(msg)
}

// Update changes the status message to the provided string
func (p *ProgressRenderer) Update(status string) {
	// Wait if needed
	timeout := p.wait.Sub(time.Now())
	if timeout.Seconds() > 0 {
		time.Sleep(timeout)
	}

	// Acquire rendering lock
	p.lock.Lock()
	defer p.lock.Unlock()

	// Check if we're already done
	if p.done {
		return
	}

	// Print the new message
	msg := "%s"
	if p.Format != "" {
		msg = p.Format
	}

	msg = fmt.Sprintf("\r"+msg, status)

	if len(msg) > p.maxLength {
		p.maxLength = len(msg)
	} else {
		fmt.Printf("\r%s", strings.Repeat(" ", p.maxLength))
	}

	fmt.Print(msg)
}

// Warn shows a temporary message instead of the status
func (p *ProgressRenderer) Warn(status string, timeout time.Duration) {
	// Acquire rendering lock
	p.lock.Lock()
	defer p.lock.Unlock()

	// Check if we're already done
	if p.done {
		return
	}

	// Render the new message
	p.wait = time.Now().Add(timeout)
	msg := fmt.Sprintf("\r%s", status)

	if len(msg) > p.maxLength {
		p.maxLength = len(msg)
	} else {
		fmt.Printf("\r%s", strings.Repeat(" ", p.maxLength))
	}

	fmt.Print(msg)
}

// UpdateProgress is a helper to update the status using an iopgress instance
func (p *ProgressRenderer) UpdateProgress(progress ioprogress.ProgressData) {
	p.Update(progress.Text)
}

// UpdateOp is a helper to update the status using a LXD API operation
func (p *ProgressRenderer) UpdateOp(op api.Operation) {
	if op.Metadata == nil {
		return
	}

	for key, value := range op.Metadata {
		if !strings.HasSuffix(key, "_progress") {
			continue
		}

		p.Update(value.(string))
		break
	}
}
