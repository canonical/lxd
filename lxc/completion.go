package main

import (
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxc/config"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// handleCompletionError should always be returned when an error occurs in a cobra.CompletionFunc.
// If the BASH_COMP_DEBUG_FILE environment variable is set, it logs the error message there before returning
// the error directive.
func handleCompletionError(err error) ([]string, cobra.ShellCompDirective) {
	cobra.CompErrorln(err.Error())
	return nil, cobra.ShellCompDirectiveError
}

// completionsFor returns completions for a given list of names, based on the partial input `toComplete`.
// If a suffix is provided, it is appended to the completion (this is useful for config keys, where an "=" can be provided).
func completionsFor(names []string, suffix string, toComplete string) []string {
	results := make([]string, 0, len(names))
	for _, n := range names {
		if strings.HasPrefix(n, toComplete) {
			results = append(results, n+suffix)
		}
	}

	return results
}

// topLevelInstanceServerResourceNameFuncs is a map of functions that can return LXD API resource names without any arguments.
// This is used when returning completions for arguments like `<remote>:<name>` where the remote is an instance server.
var topLevelInstanceServerResourceNameFuncs = map[string]func(server lxd.InstanceServer) ([]string, error){
	"certificate": func(server lxd.InstanceServer) ([]string, error) {
		return server.GetCertificateFingerprints()
	},
	"group": func(server lxd.InstanceServer) ([]string, error) {
		return server.GetAuthGroupNames()
	},
	"identity": func(server lxd.InstanceServer) ([]string, error) {
		methodToIdentifier, err := server.GetIdentityAuthenticationMethodsIdentifiers()
		if err != nil {
			return nil, err
		}

		var names []string
		for method, identifiers := range methodToIdentifier {
			for _, identifier := range identifiers {
				names = append(names, strings.Join([]string{method, identifier}, "/"))
			}
		}

		return names, nil
	},
	"identity_provider_group": func(server lxd.InstanceServer) ([]string, error) {
		return server.GetIdentityProviderGroupNames()
	},
	"instance": func(server lxd.InstanceServer) ([]string, error) {
		return server.GetInstanceNames(api.InstanceTypeAny)
	},
	"network": func(server lxd.InstanceServer) ([]string, error) {
		return server.GetNetworkNames()
	},
	"network_acl": func(server lxd.InstanceServer) ([]string, error) {
		return server.GetNetworkACLNames()
	},
	"network_zone": func(server lxd.InstanceServer) ([]string, error) {
		return server.GetNetworkZoneNames()
	},
	"profile": func(server lxd.InstanceServer) ([]string, error) {
		return server.GetProfileNames()
	},
	"project": func(server lxd.InstanceServer) ([]string, error) {
		return server.GetProjectNames()
	},
	"storage_pool": func(server lxd.InstanceServer) ([]string, error) {
		return server.GetStoragePoolNames()
	},
	"cluster_member": func(server lxd.InstanceServer) ([]string, error) {
		return server.GetClusterMemberNames()
	},
	"cluster_group": func(server lxd.InstanceServer) ([]string, error) {
		return server.GetClusterGroupNames()
	},
}

var topLevelImageServerResourceNameFuncs = map[string]func(server lxd.ImageServer) ([]string, error){
	"image": func(server lxd.ImageServer) ([]string, error) {
		return server.GetImageFingerprints()
	},
	"image_alias": func(server lxd.ImageServer) ([]string, error) {
		return server.GetImageAliasNames()
	},
}

// cmpTopLevelResource is used for general comparison of `<remote>:<resource>` arguments.
// If no `:` is present in the partial argument `toComplete`, resources from the default remote are returned alongside
// a list of remote names. The default project for the given remote is used to get the resource list.
func (g *cmdGlobal) cmpTopLevelResource(entityType string, toComplete string) ([]string, cobra.ShellCompDirective) {
	remote, partialResourceName, err := g.conf.ParseRemote(toComplete)
	if err != nil {
		return handleCompletionError(err)
	}

	names, _ := g.cmpTopLevelResourceInRemote(remote, entityType, partialResourceName)
	results := make([]string, 0, len(names)+len(g.conf.Remotes))
	for _, name := range names {
		var completion string

		if remote == g.conf.DefaultRemote {
			completion = name
		} else {
			completion = remote + ":" + name
		}

		results = append(results, completion)
	}

	directive := cobra.ShellCompDirectiveNoFileComp
	if !strings.Contains(toComplete, ":") {
		filters := instanceServerRemoteCompletionFilters(*g.conf)
		if slices.Contains([]string{"image", "image_alias"}, entityType) {
			filters = imageServerRemoteCompletionFilters(*g.conf)
		}

		remotes, directives := g.cmpRemotes(toComplete, ":", true, filters...)
		if len(remotes) > 0 {
			// Only append the no space directive if we're returning any remotes.
			results = append(results, remotes...)
			directive |= directives
		}
	}

	return results, directive
}

// cmpTopLevelResourceInRemote returns completions for a given entity type in a given remote, based on the partial `toComplete` argument.
func (g *cmdGlobal) cmpTopLevelResourceInRemote(remote string, entityType string, toComplete string) ([]string, cobra.ShellCompDirective) {
	instanceServerResourceNameFunc, ok := topLevelInstanceServerResourceNameFuncs[entityType]
	if !ok {
		imageServerResourceNameFunc, ok := topLevelImageServerResourceNameFuncs[entityType]
		if !ok {
			return handleCompletionError(fmt.Errorf("Cannot compare names: Entity type %q is not a top level resource", entityType))
		}

		server, err := g.conf.GetImageServer(remote)
		if err != nil {
			return handleCompletionError(err)
		}

		names, err := imageServerResourceNameFunc(server)
		if err != nil {
			handleCompletionError(err)
		}

		return completionsFor(names, "", toComplete), cobra.ShellCompDirectiveNoFileComp
	}

	server, err := g.conf.GetInstanceServerWithConnectionArgs(remote, &lxd.ConnectionArgs{SkipGetServer: true})
	if err != nil {
		return handleCompletionError(err)
	}

	names, err := instanceServerResourceNameFunc(server)
	if err != nil {
		return handleCompletionError(err)
	}

	return completionsFor(names, "", toComplete), cobra.ShellCompDirectiveNoFileComp
}

// configOptionAppender returns two functions, the first appends config options to a slice, the second returns the current
// slice value. Config options are trimmed to the last '.' to improve discoverability. If there is no trailing '.', the
// given suffix will be appended. This allows lists like: ["gid=", "id=", "mdev=", "mig."] when setting configuration.
func configOptionAppender(toComplete string, suffix string, size int) (func(option string), func() []string) {
	var out []string
	if size > 0 {
		out = make([]string, 0, size)
	}

	return func(option string) {
			trimmed, found := strings.CutPrefix(option, toComplete)
			if !found {
				return // Does not have prefix
			}

			var key string
			remaining, _, ok := strings.Cut(trimmed, ".")
			if ok {
				// The option contains a `.<something>` after the completion, append only up to the '.'
				key = toComplete + remaining + "."
			} else if !strings.HasSuffix(option, ".") {
				// The option does not contain anymore `.<something>` after the completion and does not end with '.', append the suffix.
				key = option + suffix
			} else {
				// The option ends with a '.', so it's a general prefix. Don't append the suffix (else we'll get "user.=").
				key = option
			}

			// Avoid duplicates.
			if !slices.Contains(out, key) {
				out = append(out, key)
			}
		}, func() []string {
			return out
		}
}

// cmpClusterMemberAllConfigKeys provides shell completion for all cluster member configuration keys.
// It takes a partial input string and returns a list of all cluster member configuration keys along with a shell completion directive.
func (g *cmdGlobal) cmpClusterMemberAllConfigKeys(memberName string) ([]string, cobra.ShellCompDirective) {
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace

	// Parse remote
	resources, err := g.ParseServers(memberName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	cluster, _, err := client.GetCluster()
	if err != nil || !cluster.Enabled {
		return nil, cobra.ShellCompDirectiveError
	}

	metadataConfiguration, err := client.GetMetadataConfiguration()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	clusterConfig, ok := metadataConfiguration.Configs["cluster"]
	if !ok {
		return nil, cobra.ShellCompDirectiveError
	}

	// Pre-allocate configKeys slice capacity.
	keyCount := 0
	for _, field := range clusterConfig {
		keyCount += len(field.Keys)
	}

	configKeys := make([]string, 0, keyCount)

	for _, field := range clusterConfig {
		for _, key := range field.Keys {
			for configKey := range key {
				configKeys = append(configKeys, configKey)
			}
		}
	}

	return configKeys, cmpDirectives
}

// cmpClusterMemberConfigs provides shell completion for cluster member configs.
// It takes a partial input string (member name) and returns a list of matching cluster member configs along with a shell completion directive.
func (g *cmdGlobal) cmpClusterMemberConfigs(memberName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(memberName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	cluster, _, err := client.GetCluster()
	if err != nil || !cluster.Enabled {
		return nil, cobra.ShellCompDirectiveError
	}

	member, _, err := client.GetClusterMember(memberName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	results := make([]string, 0, len(member.Config))
	for k := range member.Config {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpClusterMemberRoles provides shell completion for cluster member roles.
// It takes a member name and returns a list of matching cluster member roles along with a shell completion directive.
func (g *cmdGlobal) cmpClusterMemberRoles(memberName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(memberName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	cluster, _, err := client.GetCluster()
	if err != nil || !cluster.Enabled {
		return nil, cobra.ShellCompDirectiveError
	}

	member, _, err := client.GetClusterMember(memberName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return member.Roles, cobra.ShellCompDirectiveNoFileComp
}

// cmpImages provides shell completion for image fingerprints and aliases. It takes a partial input string and returns a
// list of matching image aliases and fingerprints along with a shell completion directive.
func (g *cmdGlobal) cmpImages(toComplete string, instanceServerOnly bool) ([]string, cobra.ShellCompDirective) {
	remote, partial, err := g.conf.ParseRemote(toComplete)
	if err != nil {
		return handleCompletionError(err)
	}

	client, err := g.conf.GetImageServer(remote)
	if err != nil {
		return handleCompletionError(err)
	}

	images, err := client.GetImages()
	if err != nil {
		return handleCompletionError(err)
	}

	results := make([]string, 0, len(images))
	appendResult := func(result string) {
		if !strings.HasPrefix(result, partial) {
			return
		}

		var name string
		if remote == g.conf.DefaultRemote && !strings.HasPrefix(toComplete, g.conf.DefaultRemote) {
			name = result
		} else {
			name = remote + ":" + result
		}

		results = append(results, name)
	}

	for _, image := range images {
		// Only suggest fingerprints if there are no aliases, the remote is private, and the image is not cached.
		// This is so that user provided images with no aliases can still be suggested, but we'll never suggest
		// images that have more friendly names elsewhere. E.g. we won't suggest `local:<fingerprint>` if it is a
		// cached version of `images:<alias>`.
		if len(image.Aliases) == 0 && !g.conf.Remotes[remote].Public && !image.Cached {
			// Only take the first 12 characters as it should be enough to be unique and we don't want long completions.
			appendResult(image.Fingerprint[:12])
		}

		for _, alias := range image.Aliases {
			appendResult(alias.Name)
		}
	}

	cmpDirectives := cobra.ShellCompDirectiveNoFileComp
	if !strings.Contains(toComplete, ":") {
		filters := imageServerRemoteCompletionFilters(*g.conf)
		if instanceServerOnly {
			filters = instanceServerRemoteCompletionFilters(*g.conf)
		}

		remotes, directives := g.cmpRemotes(toComplete, ":", true, filters...)
		results = append(results, remotes...)
		cmpDirectives |= directives
	}

	return results, cmpDirectives
}

// getInstanceType gets the instance type by the "lightest" means, by not requiring the server to recurse.
func (g *cmdGlobal) getInstanceType(remote string, instanceName string) api.InstanceType {
	client, err := g.conf.GetInstanceServerWithConnectionArgs(remote, &lxd.ConnectionArgs{SkipGetServer: true})
	if err != nil {
		return api.InstanceTypeAny
	}

	vmNames, err := client.GetInstanceNames(api.InstanceTypeVM)
	if err != nil {
		return api.InstanceTypeAny
	}

	if slices.Contains(vmNames, instanceName) {
		return api.InstanceTypeVM
	}

	return api.InstanceTypeContainer
}

// cmpInstanceKeysByType provides shell completion for all instance configuration keys appropriate for the given instance type.
func (g *cmdGlobal) cmpInstanceKeysByType(instanceType api.InstanceType, suffix string, toComplete string) ([]string, cobra.ShellCompDirective) {
	instanceKeys := make([]string, 0, len(instancetype.InstanceConfigKeysAny)+len(instancetype.InstanceConfigKeysVM)+len(instancetype.InstanceConfigKeysContainer))

	// Keys for all instances
	for key := range instancetype.InstanceConfigKeysAny {
		instanceKeys = append(instanceKeys, key)
	}

	// Prefixes for all instances
	instanceKeys = append(instanceKeys, instancetype.ConfigKeyPrefixesAny...)

	switch instanceType {
	case api.InstanceTypeVM:
		// VM Specific keys
		for key := range instancetype.InstanceConfigKeysVM {
			instanceKeys = append(instanceKeys, key)
		}

	case api.InstanceTypeContainer:
		// Container specific keys
		for key := range instancetype.InstanceConfigKeysContainer {
			instanceKeys = append(instanceKeys, key)
		}

		// Container specific prefixes
		instanceKeys = append(instanceKeys, instancetype.ConfigKeyPrefixesContainer...)

	default:
		// Unknown instance type (or profile), show completions for all keys and prefixes
		for key := range instancetype.InstanceConfigKeysVM {
			instanceKeys = append(instanceKeys, key)
		}

		for key := range instancetype.InstanceConfigKeysContainer {
			instanceKeys = append(instanceKeys, key)
		}

		instanceKeys = append(instanceKeys, instancetype.ConfigKeyPrefixesContainer...)
	}

	appendOption, result := configOptionAppender(toComplete, suffix, -1)
	for _, keyName := range instanceKeys {
		if shared.StringHasPrefix(keyName, "image", "volatile") {
			continue
		}

		appendOption(keyName)
	}

	return result(), cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

// cmpInstanceSetKeys provides shell completion for instance configuration keys which are currently set.
// It takes an instance name to determine instance type and returns a list of instance configuration keys along with a shell completion directive.
func (g *cmdGlobal) cmpInstanceSetKeys(remote string, instanceName string, toComplete string) ([]string, cobra.ShellCompDirective) {
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	client, err := g.conf.GetInstanceServerWithConnectionArgs(remote, &lxd.ConnectionArgs{SkipGetServer: true})
	if err != nil {
		return handleCompletionError(err)
	}

	instance, _, err := client.GetInstance(instanceName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	// Pre-allocate configKeys slice capacity.
	keyCount := len(instance.Config)
	configKeys := make([]string, 0, keyCount)

	for configKey := range instance.Config {
		if !shared.StringHasPrefix(configKey, "volatile", "image") && strings.HasPrefix(configKey, toComplete) {
			configKeys = append(configKeys, configKey)
		}
	}

	return configKeys, cmpDirectives | cobra.ShellCompDirectiveNoSpace
}

// cmpServerAllKeys provides shell completion for all server configuration keys.
// It takes a partial input string and returns a list of all server configuration keys along with a shell completion directive.
func (g *cmdGlobal) cmpServerAllKeys(remote string, suffix string, toComplete string) ([]string, cobra.ShellCompDirective) {
	client, err := g.conf.GetInstanceServerWithConnectionArgs(remote, &lxd.ConnectionArgs{SkipGetServer: true})
	if err != nil {
		return handleCompletionError(err)
	}

	metadataConfiguration, err := client.GetMetadataConfiguration()
	if err != nil {
		return handleCompletionError(err)
	}

	server, ok := metadataConfiguration.Configs["server"]
	if !ok {
		return handleCompletionError(fmt.Errorf("Remote %q metadata response did not include server configuration", remote))
	}

	keyCount := 0
	for _, field := range server {
		keyCount += len(field.Keys)
	}

	appendOption, result := configOptionAppender(toComplete, suffix, keyCount)
	for _, field := range server {
		for _, keyMap := range field.Keys {
			for key := range keyMap {
				if strings.HasPrefix(key, "volatile") {
					continue
				}

				appendOption(key)
			}
		}
	}

	return result(), cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

// cmpServerSetKeys provides shell completion for server configuration keys which are currently set.
// It takes a partial input string and returns a list of server configuration keys along with a shell completion directive.
func (g *cmdGlobal) cmpServerSetKeys(remote string, toComplete string) ([]string, cobra.ShellCompDirective) {
	client, err := g.conf.GetInstanceServerWithConnectionArgs(remote, &lxd.ConnectionArgs{SkipGetServer: true})
	if err != nil {
		return handleCompletionError(err)
	}

	server, _, err := client.GetServer()
	if err != nil {
		return handleCompletionError(err)
	}

	results := make([]string, 0, len(server.Config))
	for k := range server.Config {
		if strings.HasPrefix(k, "volatile") {
			continue
		}

		if strings.HasPrefix(k, toComplete) {
			results = append(results, k)
		}
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpInstanceConfigTemplates provides shell completion for instance config templates.
// It takes an instance name and returns a list of instance config templates along with a shell completion directive.
func (g *cmdGlobal) cmpInstanceConfigTemplates(instanceName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(instanceName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	_, instanceNameOnly, _ := strings.Cut(instanceName, ":")
	if instanceNameOnly == "" {
		instanceNameOnly = instanceName
	}

	results, err := client.GetInstanceTemplateFiles(instanceNameOnly)
	if err != nil {
		cobra.CompDebug(err.Error(), true)
		return nil, cobra.ShellCompDirectiveError
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpInstanceDeviceNames provides shell completion for instance devices.
// It takes an instance name and returns a list of instance device names along with a shell completion directive.
func (g *cmdGlobal) cmpInstanceDeviceNames(instanceName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(instanceName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	instanceNameOnly, _, err := client.GetInstance(instanceName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	results := make([]string, 0, len(instanceNameOnly.Devices))
	for k := range instanceNameOnly.Devices {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpInstanceAllDeviceTypes provides shell completion for all instance device types.
// It takes an instance name and returns a list of all possible instance devices along with a shell completion directive.
func (g *cmdGlobal) cmpInstanceAllDeviceTypes(remote string, toComplete string) ([]string, cobra.ShellCompDirective) {
	client, err := g.conf.GetInstanceServerWithConnectionArgs(remote, &lxd.ConnectionArgs{SkipGetServer: true})
	if err != nil {
		return handleCompletionError(err)
	}

	metadataConfiguration, err := client.GetMetadataConfiguration()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	devices := make([]string, 0, len(metadataConfiguration.Configs))

	for key := range metadataConfiguration.Configs {
		if !strings.HasPrefix(key, "device-") {
			continue
		}

		parts := strings.Split(key, "-")
		deviceType := parts[1]

		// "unix" is not a device, get the next part.
		if deviceType == "unix" && len(parts) > 2 {
			// The metadata API has "unix-usb", but the device type is just "usb"
			if parts[2] == "usb" {
				deviceType = "usb"
			} else {
				// Otherwise append the next part.
				deviceType += "-" + parts[2]
			}
		}

		if strings.HasPrefix(deviceType, toComplete) && !slices.Contains(devices, deviceType) {
			devices = append(devices, deviceType)
		}
	}

	return devices, cobra.ShellCompDirectiveNoFileComp
}

// cmpDeviceSubtype returns completions for device subtypes. For example, if the device type is "nic", the subtype may be "bridged".
// It is expected that the metadata API returns config keys per subtype under "device-<device_type>-<subtype>".
// A prefix may be provided so that the completed result may be used as a config key directly, e.g. "nictype=".
func (g *cmdGlobal) cmpDeviceSubtype(remote string, deviceType string, prefix string, toComplete string) ([]string, cobra.ShellCompDirective) {
	client, err := g.conf.GetInstanceServerWithConnectionArgs(remote, &lxd.ConnectionArgs{SkipGetServer: true})
	if err != nil {
		return handleCompletionError(err)
	}

	metadataConfiguration, err := client.GetMetadataConfiguration()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	subTypes := make([]string, 0, len(metadataConfiguration.Configs))
	for key := range metadataConfiguration.Configs {
		subType, ok := strings.CutPrefix(key, "device-"+deviceType+"-")
		if !ok {
			continue
		}

		if strings.HasPrefix(subType, toComplete) {
			subTypes = append(subTypes, prefix+subType)
		}
	}

	return subTypes, cobra.ShellCompDirectiveNoFileComp
}

// cmpInstanceAllDeviceOptions provides shell completion for all instance device options.
// It takes an instance name and device type and returns a list of all possible instance device options along with a shell completion directive.
// If the subtype is given, only options supported by that subtype will be returned.
func (g *cmdGlobal) cmpInstanceAllDeviceOptions(remote string, deviceType string, subtype string, toComplete string) ([]string, cobra.ShellCompDirective) {
	client, err := g.conf.GetInstanceServerWithConnectionArgs(remote, &lxd.ConnectionArgs{SkipGetServer: true})
	if err != nil {
		return handleCompletionError(err)
	}

	metadataConfiguration, err := client.GetMetadataConfiguration()
	if err != nil {
		return handleCompletionError(err)
	}

	appendOption, result := configOptionAppender(toComplete, "=", -1)
	for key, device := range metadataConfiguration.Configs {
		if !strings.HasPrefix(key, "device-") {
			continue
		}

		parts := strings.Split(key, "-")
		metadataDeviceType := parts[1]
		if metadataDeviceType == "unix" && len(parts) > 2 {
			if parts[2] == "usb" {
				metadataDeviceType = "usb"
			} else {
				metadataDeviceType += "-" + parts[2]
			}
		}

		if metadataDeviceType == deviceType {
			if subtype != "" {
				if len(parts) < 3 {
					continue
				}

				if parts[2] != subtype {
					continue
				}
			}

			conf := device["device-conf"]
			for _, keyMap := range conf.Keys {
				for option := range keyMap {
					appendOption(option)
				}
			}
		}
	}

	return result(), cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

// cmpInstancesAction provides shell completion for all instance actions (start, pause, exec, stop and delete).
// It takes a partial input string, an action, and a boolean indicating if the force flag has been passed in. It returns a list of applicable instances based on their state and the requested action, along with a shell completion directive.
func (g *cmdGlobal) cmpInstancesAction(toComplete string, action string, flagForce bool) ([]string, cobra.ShellCompDirective) {
	var results []string
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(toComplete)

	var filteredInstanceStatuses []string

	switch action {
	case "start":
		filteredInstanceStatuses = append(filteredInstanceStatuses, "Stopped", "Frozen")
	case "pause", "exec":
		filteredInstanceStatuses = append(filteredInstanceStatuses, "Running")
	case "stop":
		if flagForce {
			filteredInstanceStatuses = append(filteredInstanceStatuses, "Running", "Frozen")
		} else {
			filteredInstanceStatuses = append(filteredInstanceStatuses, "Running")
		}

	case "delete":
		if flagForce {
			filteredInstanceStatuses = append(filteredInstanceStatuses, api.GetAllStatusCodeStrings()...)
		} else {
			filteredInstanceStatuses = append(filteredInstanceStatuses, "Stopped")
		}

	default:
		filteredInstanceStatuses = append(filteredInstanceStatuses, api.GetAllStatusCodeStrings()...)
	}

	if len(resources) > 0 {
		resource := resources[0]

		instances, _ := resource.server.GetInstances("")

		results = make([]string, 0, len(instances))
		for _, instance := range instances {
			var name string

			if slices.Contains(filteredInstanceStatuses, instance.Status) {
				if resource.remote == g.conf.DefaultRemote && !strings.HasPrefix(toComplete, g.conf.DefaultRemote) {
					name = instance.Name
				} else {
					name = resource.remote + ":" + instance.Name
				}

				results = append(results, name)
			}
		}

		if !strings.Contains(toComplete, ":") {
			remotes, directives := g.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*g.conf)...)
			results = append(results, remotes...)
			cmpDirectives |= directives
		}
	}

	return completionsFor(results, "", toComplete), cmpDirectives
}

func (g *cmdGlobal) cmpSnapshotNames(remote string, instanceName string, toComplete string) ([]string, cobra.ShellCompDirective) {
	server, err := g.conf.GetInstanceServerWithConnectionArgs(remote, &lxd.ConnectionArgs{SkipGetServer: true})
	if err != nil {
		return handleCompletionError(err)
	}

	snapshotNames, err := server.GetInstanceSnapshotNames(instanceName)
	if err != nil {
		return handleCompletionError(err)
	}

	return completionsFor(snapshotNames, "", toComplete), cobra.ShellCompDirectiveNoFileComp
}

// cmpInstancesAndSnapshots provides shell completion for instances and their snapshots.
// It takes a partial input string and returns a list of matching instances and their snapshots, along with a shell completion directive.
func (g *cmdGlobal) cmpInstancesAndSnapshots(toComplete string) ([]string, cobra.ShellCompDirective) {
	results := []string{}
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp
	remote, partial, err := g.conf.ParseRemote(toComplete)
	if err != nil {
		return handleCompletionError(err)
	}

	instanceName, partialSnapshotName, isSnapshot := strings.Cut(partial, shared.SnapshotDelimiter)
	if !isSnapshot {
		return g.cmpTopLevelResource("instance", toComplete)
	}

	completions, _ := g.cmpSnapshotNames(remote, instanceName, partialSnapshotName)
	for _, snapshot := range completions {
		name := instanceName + shared.SnapshotDelimiter + snapshot
		if remote != g.conf.DefaultRemote || strings.HasPrefix(toComplete, g.conf.DefaultRemote) {
			name = remote + ":" + name
		}

		results = append(results, name)
	}

	if !strings.Contains(toComplete, ":") {
		remotes, directives := g.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*g.conf)...)
		results = append(results, remotes...)
		cmpDirectives |= directives
	}

	return completionsFor(results, "", toComplete), cmpDirectives
}

// cmpNetworkACLConfigs provides shell completion for network ACL configs.
// It takes an ACL name and returns a list of network ACL configs along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkACLConfigs(aclName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(aclName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	acl, _, err := client.GetNetworkACL(resource.name)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	results := make([]string, 0, len(acl.Config))
	for k := range acl.Config {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpNetworkACLRuleProperties provides shell completion for network ACL rule properties.
// It returns a list of network ACL rules provided by `networkACLRuleJSONStructFieldMap()“ along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkACLRuleProperties() ([]string, cobra.ShellCompDirective) {
	allowedKeys := networkACLRuleJSONStructFieldMap()
	results := make([]string, 0, len(allowedKeys))
	for key := range allowedKeys {
		results = append(results, key+"=")
	}

	return results, cobra.ShellCompDirectiveNoSpace
}

// cmpNetworkForwardConfigs provides shell completion for network forward configs.
// It takes a network name and listen address, and returns a list of network forward configs along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkForwardConfigs(networkName string, listenAddress string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(networkName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	forward, _, err := client.GetNetworkForward(networkName, listenAddress)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	results := make([]string, 0, len(forward.Config))
	for k := range forward.Config {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpNetworkForwards provides shell completion for network forwards.
// It takes a network name and returns a list of network forwards along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkForwards(networkName string) ([]string, cobra.ShellCompDirective) {
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(networkName)

	if len(resources) <= 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]

	results, err := resource.server.GetNetworkForwardAddresses(networkName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return results, cmpDirectives
}

// cmpNetworkForwardPortTargetAddresses provides shell completion for network forward port target addresses.
// It takes a network name and listen address to determine whether to return ipv4 or ipv6 target addresses and returns a list of target addresses.
func (g *cmdGlobal) cmpNetworkForwardPortTargetAddresses(networkName string, listenAddress string) ([]string, cobra.ShellCompDirective) {
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(networkName)
	if len(resources) <= 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	instances, err := resource.server.GetInstancesFull(api.InstanceTypeAny)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var results []string
	listenAddressIsIP4 := net.ParseIP(listenAddress).To4() != nil
	for _, instance := range instances {
		if instance.IsActive() && instance.State != nil && instance.State.Network != nil {
			for _, network := range instance.State.Network {
				if network.Type == "loopback" {
					continue
				}

				results = make([]string, 0, len(network.Addresses))
				for _, address := range network.Addresses {
					if slices.Contains([]string{"link", "local"}, address.Scope) {
						continue
					}

					if (listenAddressIsIP4 && address.Family == "inet") || (!listenAddressIsIP4 && address.Family == "inet6") {
						results = append(results, address.Address)
					}
				}
			}
		}
	}

	return results, cmpDirectives
}

// cmpNetworkLoadBalancers provides shell completion for network load balancers.
// It takes a network name and returns a list of network load balancers along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkLoadBalancers(networkName string) ([]string, cobra.ShellCompDirective) {
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(networkName)

	if len(resources) <= 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]

	results, err := resource.server.GetNetworkLoadBalancerAddresses(networkName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return results, cmpDirectives
}

// cmpNetworkPeerConfigs provides shell completion for network peer configs.
// It takes a network name and peer name, and returns a list of network peer configs along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkPeerConfigs(networkName string, peerName string) ([]string, cobra.ShellCompDirective) {
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(networkName)

	if len(resources) <= 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]

	peer, _, err := resource.server.GetNetworkPeer(resource.name, peerName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	results := make([]string, 0, len(peer.Config))
	for k := range peer.Config {
		results = append(results, k)
	}

	return results, cmpDirectives
}

// cmpNetworkPeers provides shell completion for network peers.
// It takes a network name and returns a list of network peers along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkPeers(networkName string) ([]string, cobra.ShellCompDirective) {
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(networkName)

	if len(resources) <= 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]

	results, err := resource.server.GetNetworkPeerNames(networkName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return results, cmpDirectives
}

// cmpNetworkConfigs provides shell completion for network configs.
// It takes a network name and returns a list of network configs along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkConfigs(networkName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(networkName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	network, _, err := client.GetNetwork(networkName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	results := make([]string, 0, len(network.Config))
	for k := range network.Config {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpNetworkInstances provides shell completion for network instances.
// It takes a network name and returns a list of instances along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkInstances(networkName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(networkName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	network, _, err := client.GetNetwork(networkName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	results := make([]string, 0, len(network.UsedBy))
	for _, i := range network.UsedBy {
		r := regexp.MustCompile(`/1.0/instances/(.*)`)
		match := r.FindStringSubmatch(i)

		if len(match) == 2 {
			results = append(results, match[1])
		}
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpNetworkProfiles provides shell completion for network profiles.
// It takes a network name and returns a list of network profiles along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkProfiles(networkName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(networkName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	network, _, err := client.GetNetwork(networkName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	results := make([]string, 0, len(network.UsedBy))
	for _, i := range network.UsedBy {
		r := regexp.MustCompile(`/1.0/profiles/(.*)`)
		match := r.FindStringSubmatch(i)

		if len(match) == 2 {
			results = append(results, match[1])
		}
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpNetworkZoneConfigs provides shell completion for network zone configs.
// It takes a zone name and returns a list of network zone configs, along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkZoneConfigs(zoneName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(zoneName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	zone, _, err := client.GetNetworkZone(zoneName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	results := make([]string, 0, len(zone.Config))
	for k := range zone.Config {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpNetworkZoneRecordConfigs provides shell completion for network zone record configs.
// It takes a zone name and record name, and returns a list of network zone record configs along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkZoneRecordConfigs(zoneName string, recordName string) ([]string, cobra.ShellCompDirective) {
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(zoneName)

	if len(resources) <= 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]

	peer, _, err := resource.server.GetNetworkZoneRecord(resource.name, recordName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	results := make([]string, 0, len(peer.Config))
	for k := range peer.Config {
		results = append(results, k)
	}

	return results, cmpDirectives
}

// cmpNetworkZoneRecords provides shell completion for network zone records.
// It takes a zone name and returns a list of network zone records along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkZoneRecords(zoneName string) ([]string, cobra.ShellCompDirective) {
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(zoneName)

	if len(resources) <= 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]

	results, err := resource.server.GetNetworkZoneRecordNames(zoneName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return results, cmpDirectives
}

// cmpProfileConfigs provides shell completion for profile configs.
// It takes a profile name and returns a list of profile configs along with a shell completion directive.
func (g *cmdGlobal) cmpProfileConfigs(profileName string) ([]string, cobra.ShellCompDirective) {
	resources, err := g.ParseServers(profileName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	profile, _, err := client.GetProfile(resource.name)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	configs := make([]string, 0, len(profile.Config))
	for c := range profile.Config {
		configs = append(configs, c)
	}

	return configs, cobra.ShellCompDirectiveNoFileComp
}

// cmpProfileDeviceNames provides shell completion for profile device names.
// It takes an instance name and returns a list of profile device names along with a shell completion directive.
func (g *cmdGlobal) cmpProfileDeviceNames(instanceName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(instanceName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	profile, _, err := client.GetProfile(resource.name)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	results := make([]string, 0, len(profile.Devices))
	for k := range profile.Devices {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpProjectConfigs provides shell completion for project configs.
// It takes a project name and returns a list of project configs along with a shell completion directive.
func (g *cmdGlobal) cmpProjectConfigs(projectName string) ([]string, cobra.ShellCompDirective) {
	resources, err := g.ParseServers(projectName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	project, _, err := client.GetProject(resource.name)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	configs := make([]string, 0, len(project.Config))
	for c := range project.Config {
		configs = append(configs, c)
	}

	return configs, cobra.ShellCompDirectiveNoFileComp
}

// remoteCompletionFilter is passed into cmpRemotes to determine which remotes to include in the result.
// Filter functions must match positively. E.g. Return true to filter the remote from the result.
type remoteCompletionFilter func(name string, remote config.Remote) bool

// filterDefault returns a function that returns true if the given remote name equals the default remote.
func filterDefaultRemote(conf config.Config) remoteCompletionFilter {
	return func(name string, remote config.Remote) bool {
		return name == conf.DefaultRemote
	}
}

// filterPublicRemotes returns true if the remote is public (e.g. cannot create instances).
func filterPublicRemotes(name string, remote config.Remote) bool {
	return remote.Public
}

// filterStaticRemotes returns true if the remote is static (cannot be modified).
func filterStaticRemotes(name string, remote config.Remote) bool {
	return remote.Static
}

// filterGlobalRemotes returns true if the remote is global (cannot be removed).
func filterGlobalRemotes(name string, remote config.Remote) bool {
	return remote.Global
}

// instanceServerRemoteCompletionFilters returns a slice of remoteCompletionFilter for use when a command requires an
// instance server only.
func instanceServerRemoteCompletionFilters(conf config.Config) []remoteCompletionFilter {
	return []remoteCompletionFilter{
		filterPublicRemotes,
		filterDefaultRemote(conf),
	}
}

// imageServerRemoteCompletionFilters returns a slice of remoteCompletionFilter for use when a command requires an image
// server (including instance servers).
func imageServerRemoteCompletionFilters(conf config.Config) []remoteCompletionFilter {
	return []remoteCompletionFilter{
		filterDefaultRemote(conf),
	}
}

// cmpRemotes provides shell completion for remotes.
// It accepts a completion string, a suffix (usually a ":"), a boolean to indicate whether the cobra.ShellCompDirectiveNoSpace should be included
// and a list of filters. The filters are used to return only applicable remotes dependant on usage. For example, if listing images we should
// complete for all remotes (image servers and instance servers). If listing instances we should return only instance servers.
func (g *cmdGlobal) cmpRemotes(toComplete string, suffix string, noSpace bool, filters ...remoteCompletionFilter) ([]string, cobra.ShellCompDirective) {
	results := []string{}

remoteLoop:
	for remoteName, rc := range g.conf.Remotes {
		if !strings.HasPrefix(remoteName, toComplete) {
			continue
		}

		for _, f := range filters {
			if f(remoteName, rc) {
				continue remoteLoop
			}
		}

		results = append(results, remoteName+suffix)
	}

	if noSpace {
		return results, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpStoragePoolConfigs provides shell completion for storage pool configs.
// It takes a storage pool name and returns a list of storage pool configs, along with a shell completion directive.
func (g *cmdGlobal) cmpStoragePoolConfigs(poolName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(poolName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	if strings.Contains(poolName, ":") {
		poolName = strings.Split(poolName, ":")[1]
	}

	pool, _, err := client.GetStoragePool(poolName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	results := make([]string, 0, len(pool.Config))
	for k := range pool.Config {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpStoragePoolWithVolume provides shell completion for storage pools and their volumes.
// It takes a partial input string and returns a list of storage pools and their volumes, along with a shell completion directive.
func (g *cmdGlobal) cmpStoragePoolWithVolume(toComplete string) ([]string, cobra.ShellCompDirective) {
	if !strings.Contains(toComplete, "/") {
		pools, compdir := g.cmpTopLevelResource("storage_pool", toComplete)
		if compdir == cobra.ShellCompDirectiveError {
			return nil, compdir
		}

		results := make([]string, 0, len(pools))
		for _, pool := range pools {
			if strings.HasSuffix(pool, ":") {
				results = append(results, pool)
			} else {
				results = append(results, pool+"/")
			}
		}

		return results, compdir | cobra.ShellCompDirectiveNoSpace
	}

	pool := strings.Split(toComplete, "/")[0]
	volumes, compdir := g.cmpStoragePoolVolumes(pool)
	if compdir == cobra.ShellCompDirectiveError {
		return nil, compdir
	}

	results := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		volName, _ := parseVolume("custom", volume)
		results = append(results, pool+"/"+volName)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpStoragePoolVolumeConfigs provides shell completion for storage pool volume configs.
// It takes a storage pool name and volume name, returns a list of storage pool volume configs, along with a shell completion directive.
func (g *cmdGlobal) cmpStoragePoolVolumeConfigs(poolName string, volumeName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(poolName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	_, pool, found := strings.Cut(poolName, ":")
	if !found {
		pool = poolName
	}

	volName, volType := parseVolume("custom", volumeName)

	volume, _, err := client.GetStoragePoolVolume(pool, volType, volName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	results := make([]string, 0, len(volume.Config))
	for k := range volume.Config {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpStoragePoolVolumeInstances provides shell completion for storage pool volume instances.
// It takes a storage pool name and volume name, returns a list of storage pool volume instances, along with a shell completion directive.
func (g *cmdGlobal) cmpStoragePoolVolumeInstances(poolName string, volumeName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(poolName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	_, pool, found := strings.Cut(poolName, ":")
	if !found {
		pool = poolName
	}

	volName, volType := parseVolume("custom", volumeName)

	volume, _, err := client.GetStoragePoolVolume(pool, volType, volName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	results := make([]string, 0, len(volume.UsedBy))
	for _, i := range volume.UsedBy {
		r := regexp.MustCompile(`/1.0/instances/(.*)`)
		match := r.FindStringSubmatch(i)

		if len(match) == 2 {
			results = append(results, match[1])
		}
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpStoragePoolVolumeProfiles provides shell completion for storage pool volume instances.
// It takes a storage pool name and volume name, returns a list of storage pool volume profiles, along with a shell completion directive.
func (g *cmdGlobal) cmpStoragePoolVolumeProfiles(poolName string, volumeName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(poolName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	_, pool, found := strings.Cut(poolName, ":")
	if !found {
		pool = poolName
	}

	volName, volType := parseVolume("custom", volumeName)

	volume, _, err := client.GetStoragePoolVolume(pool, volType, volName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	results := make([]string, 0, len(volume.UsedBy))
	for _, i := range volume.UsedBy {
		r := regexp.MustCompile(`/1.0/profiles/(.*)`)
		match := r.FindStringSubmatch(i)

		if len(match) == 2 {
			results = append(results, match[1])
		}
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpStoragePoolVolumeSnapshots provides shell completion for storage pool volume snapshots.
// It takes a storage pool name and volume name, returns a list of storage pool volume snapshots, along with a shell completion directive.
func (g *cmdGlobal) cmpStoragePoolVolumeSnapshots(poolName string, volumeName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(poolName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	_, pool, found := strings.Cut(poolName, ":")
	if !found {
		pool = poolName
	}

	volName, volType := parseVolume("custom", volumeName)

	snapshots, err := client.GetStoragePoolVolumeSnapshotNames(pool, volType, volName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return snapshots, cobra.ShellCompDirectiveNoFileComp
}

// cmpStoragePoolVolumes provides shell completion for storage pool volumes.
// It takes a storage pool name and returns a list of storage pool volumes along with a shell completion directive.
// Parameter volumeTypes determines which types of volumes are valid as completion options, none being provided means all types are valid.
func (g *cmdGlobal) cmpStoragePoolVolumes(poolName string, volumeTypes ...string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(poolName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	_, pool, found := strings.Cut(poolName, ":")
	if !found {
		pool = poolName
	}

	volumes, err := client.GetStoragePoolVolumeNames(pool)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	// If no volume type is provided, don't filter on type.
	if len(volumeTypes) == 0 {
		return volumes, cobra.ShellCompDirectiveNoFileComp
	}

	// Only complete volumes specified by volumeTypes.
	customVolumeNames := []string{}
	for _, volume := range volumes {
		// Parse snapshots returned by GetStoragePoolVolumeNames.
		volumeName, snapshotName, found := strings.Cut(volume, "/snapshots")
		if found {
			customVolumeNames = append(customVolumeNames, volumeName+snapshotName)
			continue
		}

		_, volType := parseVolume("custom", volume)
		if slices.Contains(volumeTypes, volType) {
			customVolumeNames = append(customVolumeNames, volume)
		}
	}

	return customVolumeNames, cobra.ShellCompDirectiveNoFileComp
}

func isSymlinkToDir(path string, d fs.DirEntry) bool {
	if d.Type()&fs.ModeSymlink == 0 {
		return false
	}

	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}

	return true
}

func (g *cmdGlobal) cmpLocalFiles(toComplete string, allowedExtensions []string) ([]string, cobra.ShellCompDirective) {
	var files []string
	sep := string(filepath.Separator)
	dir, prefix := filepath.Split(toComplete)
	if prefix == "." || prefix == ".." {
		files = append(files, dir+prefix+sep)
	}

	root, err := filepath.EvalSymlinks(filepath.Dir(dir))
	if err != nil {
		return handleCompletionError(err)
	}

	hasExtension := func(entry string) bool {
		if len(allowedExtensions) == 0 {
			return true
		}

		for _, e := range allowedExtensions {
			if strings.HasSuffix(entry, e) {
				return true
			}
		}

		return false
	}

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == root {
			return err
		}

		base := filepath.Base(path)
		if strings.HasPrefix(base, prefix) {
			// Match files and directories starting with the given prefix.
			file := dir + base
			switch {
			case d.IsDir():
				file += sep
			case isSymlinkToDir(path, d):
				if base == prefix {
					file += sep
				}

			default:
				if !hasExtension(file) {
					return nil
				}
			}

			files = append(files, file)
		}

		if d.IsDir() {
			return fs.SkipDir
		}

		return nil
	})

	return files, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

// cmpFiles provides shell completions for instances and files based on the input.
//
// If `includeLocalFiles` is true, it includes local file completions relative to the `toComplete` path.
func (g *cmdGlobal) cmpFiles(toComplete string, includeLocalFiles bool) ([]string, cobra.ShellCompDirective) {
	instances, directives := g.cmpTopLevelResource("instance", toComplete)
	// Append "/" to instances to indicate directory-like behavior.
	for i := range instances {
		if strings.HasSuffix(instances[i], ":") {
			continue
		}

		instances[i] += string(filepath.Separator)
	}

	directives |= cobra.ShellCompDirectiveNoSpace

	// Early return when no instances are found.
	if len(instances) == 0 {
		if includeLocalFiles {
			return nil, cobra.ShellCompDirectiveDefault
		}

		return instances, directives
	}

	// Early return when not including local files.
	if !includeLocalFiles {
		return instances, directives
	}

	files, fileDirectives := g.cmpLocalFiles(toComplete, nil)
	return append(instances, files...), directives | fileDirectives
}
