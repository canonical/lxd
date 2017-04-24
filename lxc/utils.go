package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/i18n"
)

// Lists
const (
	listFormatCSV   = "csv"
	listFormatJSON  = "json"
	listFormatTable = "table"
	listFormatYAML  = "yaml"
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

type StringList [][]string

func (a StringList) Len() int {
	return len(a)
}

func (a StringList) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a StringList) Less(i, j int) bool {
	x := 0
	for x = range a[i] {
		if a[i][x] != a[j][x] {
			break
		}
	}

	if a[i][x] == "" {
		return false
	}

	if a[j][x] == "" {
		return true
	}

	return a[i][x] < a[j][x]
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

// Used to return a user friendly error
func getLocalErr(err error) error {
	t, ok := err.(*url.Error)
	if !ok {
		return nil
	}

	u, ok := t.Err.(*net.OpError)
	if !ok {
		return nil
	}

	if u.Op == "dial" && u.Net == "unix" {
		var lxdErr error

		sysErr, ok := u.Err.(*os.SyscallError)
		if ok {
			lxdErr = sysErr.Err
		} else {
			// syscall.Errno may be returned on some systems, e.g. CentOS
			lxdErr, ok = u.Err.(syscall.Errno)
			if !ok {
				return nil
			}
		}

		switch lxdErr {
		case syscall.ENOENT, syscall.ECONNREFUSED, syscall.EACCES:
			return lxdErr
		}
	}

	return nil
}

// Add a device to a container
func containerDeviceAdd(client lxd.ContainerServer, name string, devName string, dev map[string]string) error {
	// Get the container entry
	container, etag, err := client.GetContainer(name)
	if err != nil {
		return err
	}

	// Check if the device already exists
	_, ok := container.Devices[devName]
	if ok {
		return fmt.Errorf(i18n.G("Device already exists: %s"), devName)
	}

	container.Devices[devName] = dev

	op, err := client.UpdateContainer(name, container.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Add a device to a profile
func profileDeviceAdd(client lxd.ContainerServer, name string, devName string, dev map[string]string) error {
	// Get the profile entry
	profile, profileEtag, err := client.GetProfile(name)
	if err != nil {
		return err
	}

	// Check if the device already exists
	_, ok := profile.Devices[devName]
	if ok {
		return fmt.Errorf(i18n.G("Device already exists: %s"), devName)
	}

	// Add the device to the container
	profile.Devices[devName] = dev

	err = client.UpdateProfile(name, profile.Writable(), profileEtag)
	if err != nil {
		return err
	}

	return nil
}
