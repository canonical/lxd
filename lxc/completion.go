package main

import (
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// cmpClusterGroupNames provides shell completion for cluster group names.
// It takes a partial input string and returns a list of matching names along with a shell completion directive.
func (g *cmdGlobal) cmpClusterGroupNames(toComplete string) ([]string, cobra.ShellCompDirective) {
	var results []string
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(toComplete)

	if len(resources) <= 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]

	cluster, _, err := resource.server.GetCluster()
	if err != nil || !cluster.Enabled {
		return nil, cobra.ShellCompDirectiveError
	}

	results, err = resource.server.GetClusterGroupNames()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return results, cmpDirectives
}

// cmpClusterGroups provides shell completion for cluster groups and their remotes.
// It takes a partial input string and returns a list of matching cluster groups along with a shell completion directive.
func (g *cmdGlobal) cmpClusterGroups(toComplete string) ([]string, cobra.ShellCompDirective) {
	var results []string
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(toComplete)

	if len(resources) <= 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]

	cluster, _, err := resource.server.GetCluster()
	if err != nil || !cluster.Enabled {
		return nil, cobra.ShellCompDirectiveError
	}

	groups, err := resource.server.GetClusterGroupNames()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	for _, group := range groups {
		var name string

		if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
			name = group
		} else {
			name = resource.remote + ":" + group
		}

		results = append(results, name)
	}

	if !strings.Contains(toComplete, ":") {
		remotes, directives := g.cmpRemotes(false)
		results = append(results, remotes...)
		cmpDirectives |= directives
	}

	return results, cmpDirectives
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

	var results []string
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

// cmpClusterMembers provides shell completion for cluster members.
// It takes a partial input string and returns a list of matching cluster members along with a shell completion directive.
func (g *cmdGlobal) cmpClusterMembers(toComplete string) ([]string, cobra.ShellCompDirective) {
	var results []string
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		cluster, _, err := resource.server.GetCluster()
		if err != nil || !cluster.Enabled {
			return nil, cobra.ShellCompDirectiveError
		}

		// Get the cluster members
		members, err := resource.server.GetClusterMembers()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		for _, member := range members {
			var name string

			if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
				name = member.ServerName
			} else {
				name = resource.remote + ":" + member.ServerName
			}

			results = append(results, name)
		}
	}

	if !strings.Contains(toComplete, ":") {
		remotes, directives := g.cmpRemotes(false)
		results = append(results, remotes...)
		cmpDirectives |= directives
	}

	return results, cmpDirectives
}

// cmpImages provides shell completion for image aliases.
// It takes a partial input string and returns a list of matching image aliases along with a shell completion directive.
func (g *cmdGlobal) cmpImages(toComplete string) ([]string, cobra.ShellCompDirective) {
	var results []string
	var remote string
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	if strings.Contains(toComplete, ":") {
		remote = strings.Split(toComplete, ":")[0]
	} else {
		remote = g.conf.DefaultRemote
	}

	remoteServer, _ := g.conf.GetImageServer(remote)

	images, _ := remoteServer.GetImages()

	for _, image := range images {
		for _, alias := range image.Aliases {
			var name string

			if remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
				name = alias.Name
			} else {
				name = remote + ":" + alias.Name
			}

			results = append(results, name)
		}
	}

	if !strings.Contains(toComplete, ":") {
		remotes, directives := g.cmpRemotes(true)
		results = append(results, remotes...)
		cmpDirectives |= directives
	}

	return results, cmpDirectives
}

// cmpInstanceKeys provides shell completion for all instance configuration keys.
// It takes an instance name to determine instance type and returns a list of all instance configuration keys along with a shell completion directive.
func (g *cmdGlobal) cmpInstanceKeys(instanceName string) ([]string, cobra.ShellCompDirective) {
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	// Early return when completing server keys.
	_, instanceNameOnly, found := strings.Cut(instanceName, ":")
	if instanceNameOnly == "" && found {
		return g.cmpServerAllKeys(instanceName)
	}

	resources, err := g.ParseServers(instanceName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	instance, _, err := client.GetInstance(instanceName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	// Complete keys based on instance type.
	instanceType := instance.Type

	metadataConfiguration, err := client.GetMetadataConfiguration()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	instanceConfig, ok := metadataConfiguration.Configs["instance"]
	if !ok {
		return nil, cobra.ShellCompDirectiveError
	}

	// Pre-allocate configKeys slice capacity.
	keyCount := 0
	for _, field := range instanceConfig {
		keyCount += len(field.Keys)
	}

	configKeys := make([]string, 0, keyCount)

	for _, field := range instanceConfig {
		for _, key := range field.Keys {
			for configKey, configKeyField := range key {
				configKey = strings.TrimSuffix(configKey, "*")

				// InstanceTypeAny config keys.
				if configKeyField.Condition == "" {
					configKeys = append(configKeys, configKey)
					continue
				}

				if instanceType == string(api.InstanceTypeContainer) && configKeyField.Condition == "container" {
					configKeys = append(configKeys, configKey)
				} else if instanceType == string(api.InstanceTypeVM) && configKeyField.Condition == "virtual machine" {
					configKeys = append(configKeys, configKey)
				}
			}
		}
	}

	return configKeys, cmpDirectives | cobra.ShellCompDirectiveNoSpace
}

// cmpInstanceAllKeys provides shell completion for all possible instance configuration keys.
// It returns a list of all possible instance configuration keys along with a shell completion directive.
func (g *cmdGlobal) cmpInstanceAllKeys(profileName string) ([]string, cobra.ShellCompDirective) {
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	// Parse remote
	resources, err := g.ParseServers(profileName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	metadataConfiguration, err := client.GetMetadataConfiguration()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	instanceConfig, ok := metadataConfiguration.Configs["instance"]
	if !ok {
		return nil, cobra.ShellCompDirectiveError
	}

	// Pre-allocate configKeys slice capacity.
	keyCount := 0
	for _, field := range instanceConfig {
		keyCount += len(field.Keys)
	}

	configKeys := make([]string, 0, keyCount)

	for _, field := range instanceConfig {
		for _, key := range field.Keys {
			for configKey := range key {
				configKey = strings.TrimSuffix(configKey, "*")
				configKeys = append(configKeys, configKey)
			}
		}
	}

	return configKeys, cmpDirectives | cobra.ShellCompDirectiveNoSpace
}

// cmpInstanceSetKeys provides shell completion for instance configuration keys which are currently set.
// It takes an instance name to determine instance type and returns a list of instance configuration keys along with a shell completion directive.
func (g *cmdGlobal) cmpInstanceSetKeys(instanceName string) ([]string, cobra.ShellCompDirective) {
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	// Early return when completing server keys.
	_, instanceNameOnly, found := strings.Cut(instanceName, ":")
	if instanceNameOnly == "" && found {
		return g.cmpServerAllKeys(instanceName)
	}

	resources, err := g.ParseServers(instanceName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	instance, _, err := client.GetInstance(instanceName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	// Fetch all config keys that can be set by a user.
	allInstanceConfigKeys, _ := g.cmpInstanceAllKeys(instanceName)

	// Convert slice to map[string]struct{} for O(1) lookups.
	keySet := make(map[string]struct{}, len(allInstanceConfigKeys))
	for _, key := range allInstanceConfigKeys {
		keySet[key] = struct{}{}
	}

	// Pre-allocate configKeys slice capacity.
	keyCount := len(instance.Config)
	configKeys := make([]string, 0, keyCount)

	for configKey := range instance.Config {
		// We only want to return the intersection between allInstanceConfigKeys and configKeys to avoid returning the full instance config.
		_, exists := keySet[configKey]
		if exists {
			configKeys = append(configKeys, configKey)
		}
	}

	return configKeys, cmpDirectives | cobra.ShellCompDirectiveNoSpace
}

// cmpServerAllKeys provides shell completion for all server configuration keys.
// It takes an instance name and returns a list of all server configuration keys along with a shell completion directive.
func (g *cmdGlobal) cmpServerAllKeys(instanceName string) ([]string, cobra.ShellCompDirective) {
	resources, err := g.ParseServers(instanceName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	metadataConfiguration, err := client.GetMetadataConfiguration()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	server, ok := metadataConfiguration.Configs["server"]
	if !ok {
		return nil, cobra.ShellCompDirectiveError
	}

	keyCount := 0
	for _, field := range server {
		keyCount += len(field.Keys)
	}

	keys := make([]string, 0, keyCount)

	for _, field := range server {
		for _, keyMap := range field.Keys {
			for key := range keyMap {
				keys = append(keys, key)
			}
		}
	}

	return keys, cobra.ShellCompDirectiveNoFileComp
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

	var instanceNameOnly = instanceName
	if strings.Contains(instanceName, ":") {
		instanceNameOnly = strings.Split(instanceName, ":")[1]
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

	var results []string
	for k := range instanceNameOnly.Devices {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpInstanceAllDevices provides shell completion for all instance devices.
// It takes an instance name and returns a list of all possible instance devices along with a shell completion directive.
func (g *cmdGlobal) cmpInstanceAllDevices(instanceName string) ([]string, cobra.ShellCompDirective) {
	resources, err := g.ParseServers(instanceName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	metadataConfiguration, err := client.GetMetadataConfiguration()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	devices := make([]string, 0, len(metadataConfiguration.Configs))

	for key := range metadataConfiguration.Configs {
		if strings.HasPrefix(key, "device-") {
			parts := strings.Split(key, "-")
			deviceName := parts[1]
			devices = append(devices, deviceName)
		}
	}

	return devices, cobra.ShellCompDirectiveNoFileComp
}

// cmpInstanceAllDeviceOptions provides shell completion for all instance device options.
// It takes an instance name and device name and returns a list of all possible instance device options along with a shell completion directive.
func (g *cmdGlobal) cmpInstanceAllDeviceOptions(instanceName string, deviceName string) ([]string, cobra.ShellCompDirective) {
	resources, err := g.ParseServers(instanceName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	metadataConfiguration, err := client.GetMetadataConfiguration()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	deviceOptions := make([]string, 0, len(metadataConfiguration.Configs))

	for key, device := range metadataConfiguration.Configs {
		parts := strings.Split(key, "-")
		if strings.HasPrefix(key, "device-") && parts[1] == deviceName {
			conf := device["device-conf"]
			for _, keyMap := range conf.Keys {
				for option := range keyMap {
					deviceOptions = append(deviceOptions, option)
				}
			}
		}
	}

	return deviceOptions, cobra.ShellCompDirectiveNoFileComp
}

// cmpInstances provides shell completion for all instances.
// It takes a partial input string and returns a list of matching instances along with a shell completion directive.
func (g *cmdGlobal) cmpInstances(toComplete string) ([]string, cobra.ShellCompDirective) {
	var results []string
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		instances, _ := resource.server.GetInstanceNames("")

		for _, instance := range instances {
			var name string

			if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
				name = instance
			} else {
				name = resource.remote + ":" + instance
			}

			results = append(results, name)
		}
	}

	if !strings.Contains(toComplete, ":") {
		remotes, _ := g.cmpRemotes(false)
		results = append(results, remotes...)
	}

	return results, cmpDirectives
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

		for _, instance := range instances {
			var name string

			if shared.ValueInSlice(instance.Status, filteredInstanceStatuses) {
				if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
					name = instance.Name
				} else {
					name = resource.remote + ":" + instance.Name
				}

				results = append(results, name)
			}
		}

		if !strings.Contains(toComplete, ":") {
			remotes, directives := g.cmpRemotes(false)
			results = append(results, remotes...)
			cmpDirectives |= directives
		}
	}

	return results, cmpDirectives
}

// cmpInstancesAndSnapshots provides shell completion for instances and their snapshots.
// It takes a partial input string and returns a list of matching instances and their snapshots, along with a shell completion directive.
func (g *cmdGlobal) cmpInstancesAndSnapshots(toComplete string) ([]string, cobra.ShellCompDirective) {
	results := []string{}
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		if shared.IsSnapshot(resource.name) {
			instName, _, _ := strings.Cut(resource.name, shared.SnapshotDelimiter)
			snapshots, _ := resource.server.GetInstanceSnapshotNames(instName)
			for _, snapshot := range snapshots {
				results = append(results, instName+shared.SnapshotDelimiter+snapshot)
			}
		} else {
			instances, _ := resource.server.GetInstanceNames("")
			for _, instance := range instances {
				var name string

				if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
					name = instance
				} else {
					name = resource.remote + ":" + instance
				}

				results = append(results, name)
			}
		}
	}

	if !strings.Contains(toComplete, ":") {
		remotes, directives := g.cmpRemotes(false)
		results = append(results, remotes...)
		cmpDirectives |= directives
	}

	return results, cmpDirectives
}

// cmpInstanceNamesFromRemote provides shell completion for instances for a specific remote.
// It takes a partial input string and returns a list of matching instances along with a shell completion directive.
func (g *cmdGlobal) cmpInstanceNamesFromRemote(toComplete string) ([]string, cobra.ShellCompDirective) {
	var results []string

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		containers, _ := resource.server.GetInstanceNames("container")
		results = append(results, containers...)
		vms, _ := resource.server.GetInstanceNames("virtual-machine")
		results = append(results, vms...)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
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

	var results []string
	for k := range acl.Config {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpNetworkACLs provides shell completion for network ACL's.
// It takes a partial input string and returns a list of matching network ACL's along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkACLs(toComplete string) ([]string, cobra.ShellCompDirective) {
	var results []string
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(toComplete)

	if len(resources) <= 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]

	acls, err := resource.server.GetNetworkACLNames()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	for _, acl := range acls {
		var name string

		if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
			name = acl
		} else {
			name = resource.remote + ":" + acl
		}

		results = append(results, name)
	}

	if !strings.Contains(toComplete, ":") {
		remotes, directives := g.cmpRemotes(false)
		results = append(results, remotes...)
		cmpDirectives |= directives
	}

	return results, cmpDirectives
}

// cmpNetworkACLRuleProperties provides shell completion for network ACL rule properties.
// It returns a list of network ACL rules provided by `networkACLRuleJSONStructFieldMap()“ along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkACLRuleProperties() ([]string, cobra.ShellCompDirective) {
	var results []string

	allowedKeys := networkACLRuleJSONStructFieldMap()
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

	var results []string
	for k := range forward.Config {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpNetworkForwards provides shell completion for network forwards.
// It takes a network name and returns a list of network forwards along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkForwards(networkName string) ([]string, cobra.ShellCompDirective) {
	var results []string
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

// cmpNetworkLoadBalancers provides shell completion for network load balancers.
// It takes a network name and returns a list of network load balancers along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkLoadBalancers(networkName string) ([]string, cobra.ShellCompDirective) {
	var results []string
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

// cmpNetworkPeerConfigs provides shell completion for network peer configs.
// It takes a network name and peer name, and returns a list of network peer configs along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkPeerConfigs(networkName string, peerName string) ([]string, cobra.ShellCompDirective) {
	var results []string
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

	for k := range peer.Config {
		results = append(results, k)
	}

	return results, cmpDirectives
}

// cmpNetworkPeers provides shell completion for network peers.
// It takes a network name and returns a list of network peers along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkPeers(networkName string) ([]string, cobra.ShellCompDirective) {
	var results []string
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

// cmpNetworks provides shell completion for networks.
// It takes a partial input string and returns a list of matching networks along with a shell completion directive.
func (g *cmdGlobal) cmpNetworks(toComplete string) ([]string, cobra.ShellCompDirective) {
	var results []string
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		networks, err := resource.server.GetNetworkNames()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		for _, network := range networks {
			var name string

			if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
				name = network
			} else {
				name = resource.remote + ":" + network
			}

			results = append(results, name)
		}
	}

	if !strings.Contains(toComplete, ":") {
		remotes, directives := g.cmpRemotes(false)
		results = append(results, remotes...)
		cmpDirectives |= directives
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

	var results []string
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

	var results []string
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

	var results []string
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

	var results []string
	for k := range zone.Config {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpNetworkZoneRecordConfigs provides shell completion for network zone record configs.
// It takes a zone name and record name, and returns a list of network zone record configs along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkZoneRecordConfigs(zoneName string, recordName string) ([]string, cobra.ShellCompDirective) {
	var results []string
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

	for k := range peer.Config {
		results = append(results, k)
	}

	return results, cmpDirectives
}

// cmpNetworkZoneRecords provides shell completion for network zone records.
// It takes a zone name and returns a list of network zone records along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkZoneRecords(zoneName string) ([]string, cobra.ShellCompDirective) {
	var results []string
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

// cmpNetworkZones provides shell completion for network zones.
// It takes a partial input string and returns a list of network zones along with a shell completion directive.
func (g *cmdGlobal) cmpNetworkZones(toComplete string) ([]string, cobra.ShellCompDirective) {
	var results []string
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		zones, err := resource.server.GetNetworkZoneNames()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		for _, project := range zones {
			var name string

			if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
				name = project
			} else {
				name = resource.remote + ":" + project
			}

			results = append(results, name)
		}
	}

	if !strings.Contains(toComplete, ":") {
		remotes, directives := g.cmpRemotes(false)
		results = append(results, remotes...)
		cmpDirectives |= directives
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

	var configs []string
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

	var results []string
	for k := range profile.Devices {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpProfileNamesFromRemote provides shell completion for profile names from a remote.
// It takes a partial input string and returns a list of profile names along with a shell completion directive.
func (g *cmdGlobal) cmpProfileNamesFromRemote(toComplete string) ([]string, cobra.ShellCompDirective) {
	var results []string

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		profiles, _ := resource.server.GetProfileNames()
		results = append(results, profiles...)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpProfiles provides shell completion for profiles.
// It takes a partial input string and a boolean specifying whether to include remotes or not, and returns a list of profiles along with a shell completion directive.
func (g *cmdGlobal) cmpProfiles(toComplete string, includeRemotes bool) ([]string, cobra.ShellCompDirective) {
	var results []string
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		profiles, _ := resource.server.GetProfileNames()

		for _, profile := range profiles {
			var name string

			if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
				name = profile
			} else {
				name = resource.remote + ":" + profile
			}

			results = append(results, name)
		}
	}

	if includeRemotes && !strings.Contains(toComplete, ":") {
		remotes, directives := g.cmpRemotes(false)
		results = append(results, remotes...)
		cmpDirectives |= directives
	}

	return results, cmpDirectives
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

	var configs []string
	for c := range project.Config {
		configs = append(configs, c)
	}

	return configs, cobra.ShellCompDirectiveNoFileComp
}

// cmpProjects provides shell completion for projects.
// It takes a partial input string and returns a list of projects along with a shell completion directive.
func (g *cmdGlobal) cmpProjects(toComplete string) ([]string, cobra.ShellCompDirective) {
	var results []string
	cmpDirectives := cobra.ShellCompDirectiveNoFileComp

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		projects, err := resource.server.GetProjectNames()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}

		for _, project := range projects {
			var name string

			if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
				name = project
			} else {
				name = resource.remote + ":" + project
			}

			results = append(results, name)
		}
	}

	if !strings.Contains(toComplete, ":") {
		remotes, directives := g.cmpRemotes(false)
		results = append(results, remotes...)
		cmpDirectives |= directives
	}

	return results, cmpDirectives
}

// cmpRemotes provides shell completion for remotes.
// It takes a boolean specifying whether to include all remotes or not and returns a list of remotes along with a shell completion directive.
func (g *cmdGlobal) cmpRemotes(includeAll bool) ([]string, cobra.ShellCompDirective) {
	results := make([]string, 0, len(g.conf.Remotes))

	for remoteName, rc := range g.conf.Remotes {
		if remoteName == "local" || (!includeAll && rc.Protocol != "lxd" && rc.Protocol != "") {
			continue
		}

		results = append(results, remoteName+":")
	}

	return results, cobra.ShellCompDirectiveNoSpace
}

// cmpRemoteNames provides shell completion for remote names.
// It returns a list of remote names provided by `g.conf.Remotes` along with a shell completion directive.
func (g *cmdGlobal) cmpRemoteNames() ([]string, cobra.ShellCompDirective) {
	var results []string

	for remoteName := range g.conf.Remotes {
		results = append(results, remoteName)
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

	var results []string
	for k := range pool.Config {
		results = append(results, k)
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpStoragePoolWithVolume provides shell completion for storage pools and their volumes.
// It takes a partial input string and returns a list of storage pools and their volumes, along with a shell completion directive.
func (g *cmdGlobal) cmpStoragePoolWithVolume(toComplete string) ([]string, cobra.ShellCompDirective) {
	if !strings.Contains(toComplete, "/") {
		pools, compdir := g.cmpStoragePools(toComplete, false)
		if compdir == cobra.ShellCompDirectiveError {
			return nil, compdir
		}

		var results []string
		for _, pool := range pools {
			if strings.HasSuffix(pool, ":") {
				results = append(results, pool)
			} else {
				results = append(results, pool+"/")
			}
		}

		return results, cobra.ShellCompDirectiveNoSpace
	}

	pool := strings.Split(toComplete, "/")[0]
	volumes, compdir := g.cmpStoragePoolVolumes(pool)
	if compdir == cobra.ShellCompDirectiveError {
		return nil, compdir
	}

	var results []string
	for _, volume := range volumes {
		volName, volType := parseVolume("custom", volume)
		if volType == "custom" {
			results = append(results, pool+"/"+volName)
		}
	}

	return results, cobra.ShellCompDirectiveNoFileComp
}

// cmpStoragePools provides shell completion for storage pool names.
// It takes a partial input string and a boolean indicating whether to avoid appending a space after the completion. The function returns a list of matching storage pool names and a shell completion directive.
func (g *cmdGlobal) cmpStoragePools(toComplete string, noSpace bool) ([]string, cobra.ShellCompDirective) {
	var results []string

	resources, _ := g.ParseServers(toComplete)

	if len(resources) > 0 {
		resource := resources[0]

		storagePools, _ := resource.server.GetStoragePoolNames()

		for _, storage := range storagePools {
			var name string

			if resource.remote == g.conf.DefaultRemote && !strings.Contains(toComplete, g.conf.DefaultRemote) {
				name = storage
			} else {
				name = resource.remote + ":" + storage
			}

			results = append(results, name)
		}
	}

	if !strings.Contains(toComplete, ":") {
		remotes, _ := g.cmpRemotes(false)
		results = append(results, remotes...)
	}

	if noSpace {
		return results, cobra.ShellCompDirectiveNoSpace
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

	var pool = poolName
	if strings.Contains(poolName, ":") {
		pool = strings.Split(poolName, ":")[1]
	}

	volName, volType := parseVolume("custom", volumeName)

	volume, _, err := client.GetStoragePoolVolume(pool, volType, volName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var results []string
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

	var pool = poolName
	if strings.Contains(poolName, ":") {
		pool = strings.Split(poolName, ":")[1]
	}

	volName, volType := parseVolume("custom", volumeName)

	volume, _, err := client.GetStoragePoolVolume(pool, volType, volName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var results []string
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

	var pool = poolName
	if strings.Contains(poolName, ":") {
		pool = strings.Split(poolName, ":")[1]
	}

	volName, volType := parseVolume("custom", volumeName)

	volume, _, err := client.GetStoragePoolVolume(pool, volType, volName)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var results []string
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

	var pool = poolName
	if strings.Contains(poolName, ":") {
		pool = strings.Split(poolName, ":")[1]
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
func (g *cmdGlobal) cmpStoragePoolVolumes(poolName string) ([]string, cobra.ShellCompDirective) {
	// Parse remote
	resources, err := g.ParseServers(poolName)
	if err != nil || len(resources) == 0 {
		return nil, cobra.ShellCompDirectiveError
	}

	resource := resources[0]
	client := resource.server

	var pool = poolName
	if strings.Contains(poolName, ":") {
		pool = strings.Split(poolName, ":")[1]
	}

	volumes, err := client.GetStoragePoolVolumeNames(pool)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return volumes, cobra.ShellCompDirectiveNoFileComp
}
