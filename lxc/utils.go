package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"

	"github.com/pkg/errors"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type stringList [][]string

func (a stringList) Len() int {
	return len(a)
}

func (a stringList) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a stringList) Less(i, j int) bool {
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

// Instance name sorting
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

// Add a device to a container
func containerDeviceAdd(client lxd.InstanceServer, name string, devName string, dev map[string]string) error {
	// Get the container entry
	container, etag, err := client.GetInstance(name)
	if err != nil {
		return err
	}

	// Check if the device already exists
	_, ok := container.Devices[devName]
	if ok {
		return fmt.Errorf(i18n.G("Device already exists: %s"), devName)
	}

	container.Devices[devName] = dev

	op, err := client.UpdateInstance(name, container.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Add a device to a profile
func profileDeviceAdd(client lxd.InstanceServer, name string, devName string, dev map[string]string) error {
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

	// Add the device to the instance
	profile.Devices[devName] = dev

	err = client.UpdateProfile(name, profile.Writable(), profileEtag)
	if err != nil {
		return err
	}

	return nil
}

// Create the specified image alises, updating those that already exist
func ensureImageAliases(client lxd.InstanceServer, aliases []api.ImageAlias, fingerprint string) error {
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

func getConfig(args ...string) (map[string]string, error) {
	if len(args) == 2 && !strings.Contains(args[0], "=") {
		if args[1] == "-" && !termios.IsTerminal(getStdinFd()) {
			buf, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				return nil, errors.Wrap(err, i18n.G("Can't read from stdin: %s"))
			}

			args[1] = string(buf[:])
		}

		return map[string]string{args[0]: args[1]}, nil
	}

	values := map[string]string{}

	for _, arg := range args {
		fields := strings.SplitN(arg, "=", 2)
		if len(fields) != 2 {
			return nil, fmt.Errorf("Invalid key=value configuration: %s", arg)
		}

		if fields[1] == "-" && !termios.IsTerminal(getStdinFd()) {
			buf, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				return nil, fmt.Errorf(i18n.G("Can't read from stdin: %s"), err)
			}

			fields[1] = string(buf[:])
		}

		values[fields[0]] = fields[1]
	}

	return values, nil
}
