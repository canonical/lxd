package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

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
	wait      time.Time
	done      bool
	lock      sync.Mutex
}

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

// Wait for an operation and cancel it on SIGINT/SIGTERM
func cancelableWait(op *lxd.RemoteOperation, progress *ProgressRenderer) error {
	// Signal handling
	chSignal := make(chan os.Signal)
	signal.Notify(chSignal, os.Interrupt)

	// Operation handling
	chOperation := make(chan error)
	go func() {
		chOperation <- op.Wait()
		close(chOperation)
	}()

	count := 0
	for {
		select {
		case err := <-chOperation:
			return err
		case <-chSignal:
			err := op.CancelTarget()
			if err == nil {
				return fmt.Errorf(i18n.G("Remote operation canceled by user"))
			} else {
				count++

				if count == 3 {
					return fmt.Errorf(i18n.G("User signaled us three times, exiting. The remote operation will keep running."))
				}

				if progress != nil {
					progress.Warn(fmt.Sprintf(i18n.G("%v (interrupt two more times to force)"), err), time.Second*5)
				}
			}
		}
	}
}

// Create the specified image alises, updating those that already exist
func ensureImageAliases(client lxd.ContainerServer, aliases []api.ImageAlias, fingerprint string) error {
	if len(aliases) == 0 {
		return nil
	}

	names := make([]string, len(aliases))
	for i, alias := range aliases {
		names[i] = alias.Name
	}
	sort.Strings(names)

	resp, err := client.GetImageAliases()
	if err != nil {
		return err
	}

	// Delete existing aliases that match provided ones
	for _, alias := range GetExistingAliases(names, resp) {
		err := client.DeleteImageAlias(alias.Name)
		if err != nil {
			fmt.Println(fmt.Sprintf(i18n.G("Failed to remove alias %s"), alias.Name))
		}
	}
	// Create new aliases
	for _, alias := range aliases {
		aliasPost := api.ImageAliasesPost{}
		aliasPost.Name = alias.Name
		aliasPost.Target = fingerprint
		err := client.CreateImageAlias(aliasPost)
		if err != nil {
			fmt.Println(fmt.Sprintf(i18n.G("Failed to create alias %s"), alias.Name))
		}
	}
	return nil
}

// GetExistingAliases returns the intersection between a list of aliases and all the existing ones.
func GetExistingAliases(aliases []string, allAliases []api.ImageAliasesEntry) []api.ImageAliasesEntry {
	existing := []api.ImageAliasesEntry{}
	for _, alias := range allAliases {
		name := alias.Name
		pos := sort.SearchStrings(aliases, name)
		if pos < len(aliases) && aliases[pos] == name {
			existing = append(existing, alias)
		}
	}
	return existing
}
