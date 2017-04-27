package main

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/i18n"
)

// Progress tracking
type ProgressRenderer struct {
	Format string

	maxLength int
}

func (p *ProgressRenderer) Done(msg string) {
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

func (p *ProgressRenderer) Update(status string) {
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

func (p *ProgressRenderer) UpdateProgress(progress lxd.ProgressData) {
	p.Update(progress.Text)
}

func (p *ProgressRenderer) UpdateOp(op api.Operation) {
	if op.Metadata == nil {
		return
	}

	for _, key := range []string{"fs_progress", "download_progress"} {
		value, ok := op.Metadata[key]
		if ok {
			p.Update(value.(string))
			break
		}
	}
}

// Image fingerprint and alias sorting
type SortImage [][]string

func (a SortImage) Len() int {
	return len(a)
}

func (a SortImage) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a SortImage) Less(i, j int) bool {
	if a[i][0] == a[j][0] {
		if a[i][3] == "" {
			return false
		}

		if a[j][3] == "" {
			return true
		}

		return a[i][3] < a[j][3]
	}

	if a[i][0] == "" {
		return false
	}

	if a[j][0] == "" {
		return true
	}

	return a[i][0] < a[j][0]
}

// Container name sorting
type byName [][]string

func (a byName) Len() int {
	return len(a)
}

func (a byName) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a byName) Less(i, j int) bool {
	if a[i][0] == "" {
		return false
	}

	if a[j][0] == "" {
		return true
	}

	return a[i][0] < a[j][0]
}

// Storage volume sorting
type byNameAndType [][]string

func (a byNameAndType) Len() int {
	return len(a)
}

func (a byNameAndType) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a byNameAndType) Less(i, j int) bool {
	if a[i][0] != a[j][0] {
		return a[i][0] < a[j][0]
	}

	if a[i][1] == "" {
		return false
	}

	if a[j][1] == "" {
		return true
	}

	return a[i][1] < a[j][1]
}

// Batch operations
type batchResult struct {
	err  error
	name string
}

func runBatch(names []string, action func(name string) error) []batchResult {
	chResult := make(chan batchResult, len(names))

	for _, name := range names {
		go func(name string) {
			chResult <- batchResult{action(name), name}
		}(name)
	}

	results := []batchResult{}
	for range names {
		results = append(results, <-chResult)
	}

	return results
}

// summaryLine returns the first line of the help text. Conventionally, this
// should be a one-line command summary, potentially followed by a longer
// explanation.
func summaryLine(usage string) string {
	for _, line := range strings.Split(usage, "\n") {
		if strings.HasPrefix(line, "Usage:") {
			continue
		}

		if len(line) == 0 {
			continue
		}

		return strings.TrimSuffix(line, ".")
	}

	return i18n.G("Missing summary.")
}
