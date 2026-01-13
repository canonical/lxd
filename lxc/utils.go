package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxc/config"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/termios"
)

// Batch operations.
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

	results := make([]batchResult, 0, len(names))
	for range names {
		results = append(results, <-chResult)
	}

	return results
}

// getProfileDevices retrieves devices from a list of profiles, if the list is empty the default profile is used.
func getProfileDevices(destRemote lxd.InstanceServer, serverSideProfiles []string) (map[string]map[string]string, error) {
	var profiles []string

	// If the list of profiles is empty then LXD would apply the default profile on the server side.
	if len(serverSideProfiles) == 0 {
		profiles = []string{"default"}
	} else {
		profiles = serverSideProfiles
	}

	profileDevices := make(map[string]map[string]string)

	// Get the effective expanded devices by overlaying each profile's devices in order.
	for _, profileName := range profiles {
		profile, _, err := destRemote.GetProfile(profileName)
		if err != nil {
			return nil, fmt.Errorf("Failed loading profile %q: %w", profileName, err)
		}

		maps.Copy(profileDevices, profile.Devices)
	}

	return profileDevices, nil
}

// Add a device to an instance.
func instanceDeviceAdd(client lxd.InstanceServer, name string, devName string, dev map[string]string) error {
	// Get the instance entry
	inst, etag, err := client.GetInstance(name)
	if err != nil {
		return err
	}

	// Check if the device already exists
	_, ok := inst.Devices[devName]
	if ok {
		return fmt.Errorf("Device already exists: %s", devName)
	}

	inst.Devices[devName] = dev

	op, err := client.UpdateInstance(name, inst.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Add a device to a profile.
func profileDeviceAdd(client lxd.InstanceServer, name string, devName string, dev map[string]string) error {
	// Get the profile entry
	profile, profileEtag, err := client.GetProfile(name)
	if err != nil {
		return err
	}

	// Check if the device already exists
	_, ok := profile.Devices[devName]
	if ok {
		return fmt.Errorf("Device already exists: %s", devName)
	}

	// Add the device to the instance
	profile.Devices[devName] = dev

	op, err := client.UpdateProfile(name, profile.Writable(), profileEtag)
	if err != nil {
		return err
	}

	return op.Wait()
}

// parseDeviceOverrides parses device overrides of the form "<deviceName>,<key>=<value>" into a device map.
// The resulting device map is unlikely to contain valid devices as these are simply values to be overridden.
func parseDeviceOverrides(deviceOverrideArgs []string) (map[string]map[string]string, error) {
	deviceMap := map[string]map[string]string{}
	for _, entry := range deviceOverrideArgs {
		if !strings.Contains(entry, "=") || !strings.Contains(entry, ",") {
			return nil, fmt.Errorf("Bad device override syntax, expecting <device>,<key>=<value>: %s", entry)
		}

		deviceName, deviceOverride, _ := strings.Cut(entry, ",")

		if deviceMap[deviceName] == nil {
			deviceMap[deviceName] = map[string]string{}
		}

		key, value, _ := strings.Cut(deviceOverride, "=")
		deviceMap[deviceName][key] = value
	}

	return deviceMap, nil
}

// IsAliasesSubset returns true if the first array is completely contained in the second array.
func IsAliasesSubset(a1 []api.ImageAlias, a2 []api.ImageAlias) bool {
	set := make(map[string]any)
	for _, alias := range a2 {
		set[alias.Name] = nil
	}

	for _, alias := range a1 {
		_, found := set[alias.Name]
		if !found {
			return false
		}
	}

	return true
}

// GetCommonAliases returns the common aliases between a list of aliases and all the existing ones.
func GetCommonAliases(client lxd.InstanceServer, aliases ...api.ImageAlias) ([]api.ImageAliasesEntry, error) {
	if len(aliases) == 0 {
		return nil, nil
	}

	names := make([]string, len(aliases))
	for i, alias := range aliases {
		names[i] = alias.Name
	}

	// 'GetExistingAliases' which is using 'sort.SearchStrings' requires sorted slice
	sort.Strings(names)

	resp, err := client.GetImageAliases()
	if err != nil {
		return nil, err
	}

	return GetExistingAliases(names, resp), nil
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
			return fmt.Errorf("Failed to remove alias %s: %w", alias.Name, err)
		}
	}

	// Create new aliases.
	for _, alias := range aliases {
		aliasPost := api.ImageAliasesPost{}
		aliasPost.Name = alias.Name
		aliasPost.Target = fingerprint
		err := client.CreateImageAlias(aliasPost)
		if err != nil {
			return fmt.Errorf("Failed to create alias %s: %w", alias.Name, err)
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
			buf, err := io.ReadAll(os.Stdin)
			if err != nil {
				return nil, fmt.Errorf("Can't read from stdin: %w", err)
			}

			args[1] = string(buf[:])
		}

		return map[string]string{args[0]: args[1]}, nil
	}

	values := map[string]string{}

	for _, arg := range args {
		key, value, found := strings.Cut(arg, "=")
		if !found {
			return nil, fmt.Errorf("Invalid key=value configuration: %s", arg)
		}

		if value == "-" && !termios.IsTerminal(getStdinFd()) {
			buf, err := io.ReadAll(os.Stdin)
			if err != nil {
				return nil, fmt.Errorf("Can't read from stdin: %w", err)
			}

			value = string(buf[:])
		}

		values[key] = value
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
			parent, snap, _ := api.GetParentAndSnapshotName(resource.name)

			_, _, err := resource.server.GetInstanceSnapshot(parent, snap)
			if err != nil {
				return fmt.Errorf("Failed checking instance snapshot exists \"%s:%s\": %w", resource.remote, resource.name, err)
			}

			continue
		}

		_, _, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return fmt.Errorf("Failed checking instance exists \"%s:%s\": %w", resource.remote, resource.name, err)
		}
	}

	return nil
}

// structHasField checks if specified struct includes field with given name.
func structHasField(typ reflect.Type, field string) bool {
	var parent reflect.Type

	for i := range typ.NumField() {
		fieldType := typ.Field(i)
		yaml := fieldType.Tag.Get("yaml")

		if yaml == ",inline" {
			parent = fieldType.Type
		}

		if yaml == field {
			return true
		}
	}

	if parent != nil {
		return structHasField(parent, field)
	}

	return false
}

// getServerSupportedFilters returns two lists: one with filters supported by server and second one with not supported.
func getServerSupportedFilters(filters []string, i any) (supportedFilters []string, unsupportedFilters []string) {
	supportedFilters = []string{}
	unsupportedFilters = []string{}

	for _, filter := range filters {
		membs := strings.SplitN(filter, "=", 2)
		// Only key/value pairs are supported by server side API
		// Only keys which are part of struct are supported by server side API
		// Multiple values (separated by ',') are not supported by server side API
		// Keys with '.' in name are not supported
		if len(membs) < 2 || !structHasField(reflect.TypeOf(i), membs[0]) || strings.Contains(membs[1], ",") || strings.Contains(membs[0], ".") {
			unsupportedFilters = append(unsupportedFilters, filter)
			continue
		}

		supportedFilters = append(supportedFilters, filter)
	}

	return supportedFilters, unsupportedFilters
}

// guessImage checks that the image name (provided by the user) is correct given an instance remote and image remote.
func guessImage(conf *config.Config, d lxd.InstanceServer, instRemote string, imgRemote string, imageRef string) (imageRemote string, image string) {
	if instRemote != imgRemote {
		return imgRemote, imageRef
	}

	fields := strings.SplitN(imageRef, "/", 2)
	_, ok := conf.Remotes[fields[0]]
	if !ok {
		return imgRemote, imageRef
	}

	_, _, err := d.GetImageAlias(imageRef)
	if err == nil {
		return imgRemote, imageRef
	}

	_, _, err = d.GetImage(imageRef)
	if err == nil {
		return imgRemote, imageRef
	}

	if len(fields) == 1 {
		fmt.Fprintf(os.Stderr, "The local image '%s' couldn't be found, trying '%s:' instead.\n", imageRef, fields[0])
		return fields[0], "default"
	}

	fmt.Fprintf(os.Stderr, "The local image '%s' couldn't be found, trying '%s:%s' instead.\n", imageRef, fields[0], fields[1])
	return fields[0], fields[1]
}

// getImgInfo returns an image server and image info for the given image name (given by a user)
// an image remote and an instance remote.
func getImgInfo(d lxd.InstanceServer, conf *config.Config, imgRemote string, instRemote string, imageRef string, source *api.InstanceSource) (lxd.ImageServer, *api.Image, error) {
	var imgRemoteServer lxd.ImageServer
	var imgInfo *api.Image
	var err error

	// Connect to the image server
	if imgRemote == instRemote {
		imgRemoteServer = d
	} else {
		imgRemoteServer, err = conf.GetImageServer(imgRemote)
		if err != nil {
			return nil, nil, err
		}
	}

	// Optimisation for simplestreams
	if conf.Remotes[imgRemote].Protocol == "simplestreams" {
		imgInfo = &api.Image{}
		imgInfo.Fingerprint = imageRef
		imgInfo.Public = true
		source.Alias = imageRef
	} else {
		// Attempt to resolve an image alias
		alias, _, err := imgRemoteServer.GetImageAlias(imageRef)
		if err == nil {
			source.Alias = imageRef
			imageRef = alias.Target
		}

		// Get the image info
		imgInfo, _, err = imgRemoteServer.GetImage(imageRef)
		if err != nil {
			return nil, nil, fmt.Errorf("Failed to find image %q on remote %q", imageRef, imgRemote)
		}
	}

	return imgRemoteServer, imgInfo, nil
}

// getExportVersion returns the version sent to the server when exporting instances and custom storage volumes.
func getExportVersion(d lxd.InstanceServer, flag string) (uint32, error) {
	backupVersionSupported := d.HasExtension("backup_metadata_version")

	// Don't allow explicitly setting 0 as it will implicitly create a backup using version 1.
	if flag == "0" {
		return 0, errors.New(`Invalid export version "0"`)
	}

	// In case no version is set, default to 0 so we can convert it to an uint32.
	if flag == "" {
		flag = "0"
	}

	versionUint, err := strconv.ParseUint(flag, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("Invalid export version %q: %w", flag, err)
	}

	versionUint32 := uint32(versionUint)

	// If the server supports setting the backup version, set the selected version.
	// If supported but the version is not set, the server picks its default version.
	// If unsupported but the version is set to 1, the field isn't set so its up to the server to pick the old version.
	if backupVersionSupported {
		if versionUint32 != 0 {
			return versionUint32, nil
		}
	} else if !backupVersionSupported && versionUint32 > api.BackupMetadataVersion1 {
		// Any version beyond 1 isn't supported by an older server without the backup_metadata_version extension.
		return 0, errors.New("The server doesn't support setting the metadata format version")
	}

	// No export version was provided.
	// Return 0 as it indicates the version is unset and it's up to the server to decide the version.
	return 0, nil
}

// newLocationHeaderTransportWrapper returns a new transport wrapper that can be used to inspect the `Location` header
// upon the response of a resource creation request to LXD.
func newLocationHeaderTransportWrapper() (*locationHeaderTransport, func(transport *http.Transport) lxd.HTTPTransporter) {
	transporter := &locationHeaderTransport{}
	return transporter, func(transport *http.Transport) lxd.HTTPTransporter {
		transporter.transport = transport
		return transporter
	}
}

// locationHeaderTransport implements lxd.HTTPTransporter by wrapping a http.Transport.
type locationHeaderTransport struct {
	transport *http.Transport
	location  string
}

// RoundTrip implements http.RoundTripper for locationHeaderTransport. It extracts the `Location` header from the HTTP
// response for later use. This is useful when the resource is not known in advance (e.g. for auto-allocated IP addresses
// of network forwards and load-balancers).
func (c *locationHeaderTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := c.transport.RoundTrip(r)
	if err != nil {
		return nil, err
	}

	c.location = resp.Header.Get("Location")

	return resp, err
}

// Transport returns the underlying transport of cloudInstanceServerTransport (to implement lxd.HTTPTransporter).
func (c *locationHeaderTransport) Transport() *http.Transport {
	return c.transport
}
