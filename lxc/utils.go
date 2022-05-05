package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

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

// Add a device to an instance
func instanceDeviceAdd(client lxd.InstanceServer, name string, devName string, dev map[string]string) error {
	// Get the instance entry
	inst, etag, err := client.GetInstance(name)
	if err != nil {
		return err
	}

	// Check if the device already exists
	_, ok := inst.Devices[devName]
	if ok {
		return fmt.Errorf(i18n.G("Device already exists: %s"), devName)
	}

	inst.Devices[devName] = dev

	op, err := client.UpdateInstance(name, inst.Writable(), etag)
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

// Create the specified image aliases, updating those that already exist.
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
			return fmt.Errorf(i18n.G("Failed to remove alias %s: %w"), alias.Name, err)
		}
	}

	// Create new aliases.
	for _, alias := range aliases {
		aliasPost := api.ImageAliasesPost{}
		aliasPost.Name = alias.Name
		aliasPost.Target = fingerprint
		err := client.CreateImageAlias(aliasPost)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to create alias %s: %w"), alias.Name, err)
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
				return nil, fmt.Errorf(i18n.G("Can't read from stdin: %w"), err)
			}

			args[1] = string(buf[:])
		}

		return map[string]string{args[0]: args[1]}, nil
	}

	values := map[string]string{}

	for _, arg := range args {
		fields := strings.SplitN(arg, "=", 2)
		if len(fields) != 2 {
			return nil, fmt.Errorf(i18n.G("Invalid key=value configuration: %s"), arg)
		}

		if fields[1] == "-" && !termios.IsTerminal(getStdinFd()) {
			buf, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				return nil, fmt.Errorf(i18n.G("Can't read from stdin: %w"), err)
			}

			fields[1] = string(buf[:])
		}

		values[fields[0]] = fields[1]
	}

	return values, nil
}

func usage(name string, args ...string) string {
	if len(args) == 0 {
		return name
	}

	return name + " " + args[0]
}

// instancesExist iterates over a list of instances (or snapshots) and checks that they exist.
func instancesExist(resources []remoteResource) error {
	for _, resource := range resources {
		// Handle snapshots.
		if shared.IsSnapshot(resource.name) {
			parent, snap, _ := shared.InstanceGetParentAndSnapshotName(resource.name)

			_, _, err := resource.server.GetInstanceSnapshot(parent, snap)
			if err != nil {
				return fmt.Errorf(i18n.G("Failed to validate \"%s:%s\": %w"), resource.remote, resource.name, err)
			}

			return nil
		}

		_, _, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to validate \"%s:%s\": %w"), resource.remote, resource.name, err)
		}
	}

	return nil
}
