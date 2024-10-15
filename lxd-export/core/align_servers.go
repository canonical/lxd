package core

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	lxd "github.com/canonical/lxd/client"
	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	nodeConfig "github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/r3labs/diff/v3"
)

type clusterTopology struct {
	ClusterGroups  []api.ClusterGroup  `diff:"cluster_groups"`
	ClusterMembers []api.ClusterMember `diff:"cluster_members"`
	ServerConfigs  []api.Server        `diff:"server_configs"`
}

func newClusterTopology(client lxd.InstanceServer) (*clusterTopology, error) {
	clusterConf, _, err := client.GetCluster()
	if err != nil {
		return nil, err
	}

	if !clusterConf.Enabled {
		return nil, errors.New("Creating an export DAG requires a cluster to be enabled")
	}

	clusterMembers, err := client.GetClusterMembers()
	if err != nil {
		return nil, err
	}

	// We don't need this field to represent the inner data in the dependency graph.
	modifiedClusterMembers := make([]api.ClusterMember, 0)
	for _, member := range clusterMembers {
		m := member
		m.Database = false // not predictable from cluster to cluster (runtime related)
		m.Message = ""     // same as above.
		m.URL = ""         // same as above (the public cluster member URL might be different according to the external network)

		modifiedClusterMembers = append(modifiedClusterMembers, m)
	}

	sort.Slice(modifiedClusterMembers, func(i, j int) bool {
		return modifiedClusterMembers[i].ServerName < modifiedClusterMembers[j].ServerName
	})

	clusterGroups, err := client.GetClusterGroups()
	if err != nil {
		return nil, err
	}

	sort.Slice(clusterGroups, func(i, j int) bool {
		return clusterGroups[i].Name < clusterGroups[j].Name
	})

	serverConfigs := make([]api.Server, 0)
	for _, member := range modifiedClusterMembers {
		specificClient := client.UseTarget(member.ServerName)
		server, _, err := specificClient.GetServer()
		if err != nil {
			return nil, err
		}

		// Auth, AuthUserName, AuthUserMethod are client specific and not predictable from cluster to cluster
		server.Auth = ""
		server.AuthUserName = ""
		server.AuthUserMethod = ""

		// A source cluster might be exported to a target cluster with a
		// different current project name (the project name is not predictable)
		server.Environment.Project = "default"
		// The server pid is not predictable from cluster to cluster
		server.Environment.ServerPid = 0

		serverConfigs = append(serverConfigs, *server)
	}

	return &clusterTopology{
		ClusterGroups:  clusterGroups,
		ClusterMembers: clusterMembers,
		ServerConfigs:  serverConfigs,
	}, nil
}

type diagSev string

const (
	critical diagSev = "critical"
	warning  diagSev = "warning"
	ok       diagSev = "ok"
)

type diagnostic struct {
	msg      string
	severity diagSev
}

type diagnostics []diagnostic

func (d diagnostics) String() string {
	s := "DIAGNOSTICS\n"
	fail := false
	for _, diag := range d {
		if diag.severity == critical {
			fail = true
		}

		s += fmt.Sprintf("- %s: %s\n", diag.severity, diag.msg)
	}

	if fail {
		s += "Cluster alignment failed\n"
	} else {
		s += "OK\n"
	}

	return s
}

func newDiagnostic(change diff.Change, severity diagSev) diagnostic {
	msg := ""
	switch {
	case change.Type == diff.UPDATE:
		msg = fmt.Sprintf("Updated from %v to %v in target at %q. ", change.From, change.To, strings.Join(change.Path, "/"))
	case change.Type == diff.DELETE:
		msg = fmt.Sprintf("Deleted %v in target at %q. ", change.From, strings.Join(change.Path, "/"))
	case change.Type == diff.CREATE:
		msg = fmt.Sprintf("Created %v in target at %q. ", change.To, strings.Join(change.Path, "/"))
	}

	if severity == critical {
		msg += "This change is critical and is not permitted."
	} else if severity == warning {
		msg += "This change is a warning."
	}

	return diagnostic{
		msg:      msg,
		severity: severity,
	}
}

// alignment represents a step in the cluster alignment process.
type alignment struct {
	desc   string
	fn     func() error
	pretty string
}

func newAlignment(desc string, payload any, f func() error) alignment {
	pretty := ""
	if payload != nil {
		pretty = logger.Pretty(payload)
	}

	return alignment{
		desc:   desc,
		fn:     f,
		pretty: pretty,
	}
}

func showAlignmentsAndDiags(diags diagnostics, alignments []alignment) string {
	var reset = "\033[0m"
	var red = "\033[31m"
	var green = "\033[32m"
	var yellow = "\033[33m"
	var blue = "\033[34m"

	out := ""
	// First, show diagnostics if any.
	if len(diags) > 0 {
		out = red + diags.String() + reset
		out += "\n\n"
	}

	if len(alignments) != 0 {
		out += green + "Server alignments:" + reset + "\n\n"
	} else {
		out += green + "No alignments" + reset + "\n"
		return out
	}

	// Then, show the planned alignments.
	out += yellow + "- Alignment 0:" + reset + "\n"

	for _, a := range alignments {
		out += a.desc
		if a.pretty != "" {
			out += fmt.Sprintf(blue+"\t%s"+reset+"\n", a.pretty)
		}

		out += "\n"
	}

	return out
}

func executeAlignments(alignments []alignment) error {
	for _, a := range alignments {
		fmt.Println("Executing alignment: ", a.desc)
		err := a.fn()
		if err != nil {
			return fmt.Errorf("Failed to execute alignment %q: %v", a.desc, err)
		}
	}

	return nil
}

var diffPathWithTags = regexp.MustCompile(`(?m)([\w]+)\((.*)\)`)
var diffTags = regexp.MustCompile(`(?m)([^;\s]+)=([^;\s]+)`)

func hasDiffTags(pathElt string) bool {
	return diffPathWithTags.MatchString(pathElt)
}

func extractStructuredDiffPathElt(pathElt string) (prefix string, tags map[string]string, err error) {
	if !hasDiffTags(pathElt) {
		return pathElt, nil, nil
	}

	parts := diffPathWithTags.FindStringSubmatch(pathElt)
	if len(parts) != 3 {
		return "", nil, fmt.Errorf("Failed to extract structured diff path from %q", pathElt)
	}

	prefix = parts[0]
	tags = make(map[string]string)
	tagParts := diffTags.FindAllStringSubmatch(parts[1], -1)
	for _, tagPart := range tagParts {
		tags[tagPart[0]] = tagPart[1]
	}

	return prefix, tags, nil
}

func isDiffPathCritical(diffPath []string) ([]string, bool, error) {
	// formattedDiffPath is the diff path without the tags
	formattedDiffPath := make([]string, 0)
	for _, pElt := range diffPath {
		prefix, tags, err := extractStructuredDiffPathElt(pElt)
		if err != nil {
			return formattedDiffPath, false, err
		}

		formattedDiffPath = append(formattedDiffPath, prefix)
		if tags != nil {
			severity, ok := tags["severity"]
			if ok && severity == "critical" {
				return formattedDiffPath, true, nil
			}
		}
	}

	return formattedDiffPath, false, nil
}

func isDiffPathWarning(diffPath []string) ([]string, bool, error) {
	formattedDiffPath := make([]string, 0)
	for _, pElt := range diffPath {
		prefix, tags, err := extractStructuredDiffPathElt(pElt)
		if err != nil {
			return formattedDiffPath, false, err
		}

		formattedDiffPath = append(formattedDiffPath, prefix)
		if tags != nil {
			severity, ok := tags["severity"]
			if ok && severity == "warning" {
				return formattedDiffPath, true, nil
			}
		}
	}

	return formattedDiffPath, false, nil
}

func compareClusterTopologies(srcTopo *clusterTopology, dstTopo *clusterTopology, dstClient lxd.InstanceServer) (alignments []alignment, diags diagnostics, err error) {
	differ, err := diff.NewDiffer(diff.DisableStructValues())
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to create differ to compare cluster configs: %w", err)
	}

	changelog, err := differ.Diff(srcTopo, dstTopo)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to diff cluster topologies: %w", err)
	}

	if changelog == nil {
		fmt.Println("No differences found between the source and destination cluster topologies")
		return nil, nil, nil
	}

	// - critical diff found: we need to error out as the reconciliation needs to change the
	// server configuration themselves.
	// - warning diff found: the warning will be logged as a path of the diagnostics but no actions
	// will be taken to reconcile the differences. These are considered ok
	// to be different between the source and target clusters but the user should be aware of them.
	alignments = make([]alignment, 0)

	// Split the global root changelog into cluster groups, cluster members and server configs changelogs.
	clusterGroupChanges := make([]diff.Change, 0)
	clusterMemberChanges := make([]diff.Change, 0)
	serverConfigChanges := make([]diff.Change, 0)
	for _, change := range changelog {
		switch change.Path[0] {
		case "cluster_groups":
			clusterGroupChanges = append(clusterGroupChanges, change)
		case "cluster_members":
			clusterMemberChanges = append(clusterMemberChanges, change)
		case "server_configs":
			serverConfigChanges = append(serverConfigChanges, change)
		default:
			return nil, nil, fmt.Errorf("Unknown root node diff path %q", change.Path[0])
		}
	}

	diags = make([]diagnostic, 0)

	// First, process the server config changes as some changes might be critical and we might error out early.
	criticalFound := false
	globalServerPut := api.ServerPut{Config: make(map[string]any)}
	localServerPuts := make(map[string]*api.ServerPut)
	for _, change := range serverConfigChanges {
		if len(change.Path) == 2 {
			if change.Type == diff.DELETE {
				return nil, nil, fmt.Errorf("Cannot have a less servers on the target cluster than on the source cluster")
			}

			return nil, nil, fmt.Errorf("Cannot have more servers on the target cluster than on the source cluster")
		}

		// Check if the diff path has critical differences
		formattedDiffPath, critic, err := isDiffPathCritical(change.Path)
		if err != nil {
			return nil, nil, err
		}

		// If there are critical differences, set criticalFound to true and add the diagnostic to the diagnostics list.
		// We'll error out at the end but we want to keep track of all the critical/warning differences that make up a diagnostic.
		if critic {
			change.Path = formattedDiffPath
			diags = append(diags, newDiagnostic(change, critical))
			criticalFound = true
		}

		formattedDiffPath, warn, err := isDiffPathWarning(change.Path)
		if err != nil {
			return nil, nil, err
		}

		if warn {
			change.Path = formattedDiffPath
			diags = append(diags, newDiagnostic(change, warning))
		}

		if criticalFound {
			continue
		}

		// Process 'config' changes in the server configs.
		if change.Path[3] == "config" {
			configKey := change.Path[4]
			v, ok := clusterConfig.ConfigSchema[configKey]
			if ok {
				// This is a global scope config key.
				switch change.Type {
				case diff.UPDATE:
					globalServerPut.Config[configKey] = change.From.(string)
				case diff.DELETE:
					globalServerPut.Config[configKey] = change.From.(string)
				case diff.CREATE:
					globalServerPut.Config[configKey] = v.Default
				}
			}

			v, ok = nodeConfig.ConfigSchema[configKey]
			if ok {
				// Get target name
				idxServer, err := strconv.Atoi(change.Path[1])
				if err != nil {
					return nil, nil, err
				}

				localServerPut, ok := localServerPuts[dstTopo.ServerConfigs[idxServer].Environment.ServerName]
				if !ok {
					localServerPut = &api.ServerPut{Config: make(map[string]any)}
					localServerPuts[dstTopo.ServerConfigs[idxServer].Environment.ServerName] = localServerPut
				}

				// This is a local scope config key.
				switch change.Type {
				case diff.UPDATE:
					localServerPut.Config[configKey] = change.From.(string)
				case diff.DELETE:
					localServerPut.Config[configKey] = change.From.(string)
				case diff.CREATE:
					localServerPut.Config[configKey] = v.Default
				}
			}
		}
	}

	if criticalFound {
		return nil, nil, fmt.Errorf("Critical differences found in the cluster topologies:\n%s", diagnostics(diags).String())
	}

	// Create the server edits.
	alignments = append(alignments, newAlignment("Update global server config", globalServerPut, func() error {
		err = dstClient.UpdateServer(globalServerPut, "")
		if err != nil {
			return err
		}

		return nil
	}))

	for serverName, serverPut := range localServerPuts {
		alignments = append(alignments, newAlignment(fmt.Sprintf("Update local server %q config", serverName), *serverPut, func() error {
			targetedClient := dstClient.UseTarget(serverName)

			err = targetedClient.UpdateServer(*serverPut, "")
			if err != nil {
				return err
			}

			return nil
		}))
	}

	// Then, process the cluster group changes as some cluster members might depend on the groups.
	clusterGroupsToUpdate := make(map[string]api.ClusterGroupPut, 0)
	clusterGroupsToAdd := make([]api.ClusterGroupsPost, 0)
	clusterGroupsToDelete := make([]string, 0)
	clusterGroupsToRename := make(map[string]string, 0)
	for _, change := range clusterGroupChanges {
		if len(change.Path) == 2 {
			switch change.Type {
			case diff.DELETE:
				group, ok := change.From.(api.ClusterGroup)
				if !ok {
					return nil, nil, fmt.Errorf("Failed to cast cluster group to ClusterGroupsPost (delete diff detected)")
				}

				clusterGroupsToAdd = append(
					clusterGroupsToAdd,
					api.ClusterGroupsPost{
						ClusterGroupPut: api.ClusterGroupPut{
							Description: group.Description,
							Members:     group.Members,
						},
						Name: group.Name,
					},
				)
			case diff.CREATE:
				group, ok := change.To.(api.ClusterGroup)
				if !ok {
					return nil, nil, fmt.Errorf("Failed to cast cluster group to ClusterGroupsPost (create diff detected)")
				}

				clusterGroupsToDelete = append(clusterGroupsToDelete, group.Name)
			}
		} else if len(change.Path) == 3 {
			idxClusterGroup, err := strconv.Atoi(change.Path[1])
			if err != nil {
				return nil, nil, err
			}

			name := dstTopo.ClusterGroups[idxClusterGroup].Name
			clusterGroupToUpdate, ok := clusterGroupsToUpdate[name]
			if !ok {
				clusterGroupToUpdate = api.ClusterGroupPut{}
			}

			switch change.Path[2] {
			case "name":
				switch change.Type {
				case diff.UPDATE:
					clusterGroupsToRename[name] = change.From.(string)
				}
			case "description":
				switch change.Type {
				case diff.UPDATE, diff.DELETE:
					description := change.From.(string)
					clusterGroupToUpdate.Description = description
					clusterGroupsToUpdate[name] = clusterGroupToUpdate
				case diff.CREATE:
					description := ""
					clusterGroupToUpdate.Description = description
					clusterGroupsToUpdate[name] = clusterGroupToUpdate
				}
			case "members":
				idxMember, err := strconv.Atoi(change.Path[3])
				if err != nil {
					return nil, nil, err
				}

				switch change.Type {
				case diff.UPDATE:
					members := dstTopo.ClusterGroups[idxClusterGroup].Members
					members[idxMember] = change.From.(string)
					clusterGroupToUpdate.Members = members
					clusterGroupsToUpdate[name] = clusterGroupToUpdate
				case diff.DELETE:
					members := dstTopo.ClusterGroups[idxClusterGroup].Members
					members = append(members[:idxMember], change.From.(string))
					members = append(members, members[idxMember+1:]...)
					clusterGroupToUpdate.Members = members
					clusterGroupsToUpdate[name] = clusterGroupToUpdate
				case diff.CREATE:
					members := dstTopo.ClusterGroups[idxClusterGroup].Members
					members = append(members[:idxMember], members[idxMember+1:]...)
					clusterGroupToUpdate.Members = members
					clusterGroupsToUpdate[name] = clusterGroupToUpdate
				}
			}
		}
	}

	// Create the cluster group edits.
	for _, group := range clusterGroupsToAdd {
		alignments = append(alignments, newAlignment(fmt.Sprintf("Add cluster group %q", group.Name), group, func() error {
			err = dstClient.CreateClusterGroup(group)
			if err != nil {
				return err
			}

			return nil
		}))
	}

	for _, group := range clusterGroupsToDelete {
		alignments = append(alignments, newAlignment(fmt.Sprintf("Delete cluster group %q", group), group, func() error {
			err = dstClient.DeleteClusterGroup(group)
			if err != nil {
				return err
			}

			return nil
		}))
	}

	for name, group := range clusterGroupsToUpdate {
		alignments = append(alignments, newAlignment(fmt.Sprintf("Update cluster group %q", name), group, func() error {
			err = dstClient.UpdateClusterGroup(name, group, "")
			if err != nil {
				return err
			}

			return nil
		}))
	}

	for name, newName := range clusterGroupsToRename {
		alignments = append(alignments, newAlignment(fmt.Sprintf("Rename cluster group %q to %q", name, newName), nil, func() error {
			err = dstClient.RenameClusterGroup(name, api.ClusterGroupPost{Name: newName})
			if err != nil {
				return err
			}

			return nil
		}))
	}

	// Finally, process the cluster member changes.
	clusterMemberPuts := make(map[string]api.ClusterMemberPut, 0)
	clusterMemberRename := make(map[string]string, 0)
	for _, change := range clusterMemberChanges {
		idxClusterMem, err := strconv.Atoi(change.Path[1])
		if err != nil {
			return nil, nil, err
		}

		serverName := dstTopo.ClusterMembers[idxClusterMem].ServerName
		clusterMemberPut, ok := clusterMemberPuts[serverName]
		if !ok {
			clusterMemberPut = api.ClusterMemberPut{}
		}

		switch change.Path[2] {
		case "server_name":
			if change.Type == diff.UPDATE {
				clusterMemberRename[serverName] = change.From.(string)
			}
		case "description":
			switch change.Type {
			case diff.UPDATE, diff.DELETE:
				clusterMemberPut.Description = change.From.(string)
				clusterMemberPuts[serverName] = clusterMemberPut
			case diff.CREATE:
				clusterMemberPut.Description = ""
				clusterMemberPuts[serverName] = clusterMemberPut
			}
		case "failure_domain":
			switch change.Type {
			case diff.UPDATE, diff.DELETE:
				clusterMemberPut.FailureDomain = change.From.(string)
				clusterMemberPuts[serverName] = clusterMemberPut
			case diff.CREATE:
				clusterMemberPut.FailureDomain = "default"
				clusterMemberPuts[serverName] = clusterMemberPut
			}
		case "roles":
			idxClusterRoles, err := strconv.Atoi(change.Path[4])
			if err != nil {
				return nil, nil, err
			}

			switch change.Type {
			case diff.UPDATE:
				roles := dstTopo.ClusterMembers[idxClusterMem].Roles
				roles[idxClusterRoles] = change.From.(string)
				clusterMemberPut.Roles = roles
				clusterMemberPuts[serverName] = clusterMemberPut
			case diff.DELETE:
				roles := dstTopo.ClusterMembers[idxClusterMem].Roles
				roles = append(roles[:idxClusterRoles], change.From.(string))
				roles = append(roles, roles[idxClusterRoles+1:]...)
				clusterMemberPut.Roles = roles
				clusterMemberPuts[serverName] = clusterMemberPut
			case diff.CREATE:
				roles := dstTopo.ClusterMembers[idxClusterMem].Roles
				roles = append(roles[:idxClusterRoles], roles[idxClusterRoles+1:]...)
				clusterMemberPut.Roles = roles
				clusterMemberPuts[serverName] = clusterMemberPut
			}
		case "config":
			key := change.Path[4]
			if clusterMemberPut.Config == nil {
				clusterMemberPut.Config = make(map[string]string)
			}

			switch change.Type {
			case diff.UPDATE, diff.DELETE:
				clusterMemberPut.Config[key] = change.From.(string)
				clusterMemberPuts[serverName] = clusterMemberPut
			case diff.CREATE:
				clusterMemberPut.Config[key] = ""
				clusterMemberPuts[serverName] = clusterMemberPut
			}
		case "groups":
			idxClusterGroups, err := strconv.Atoi(change.Path[4])
			if err != nil {
				return nil, nil, err
			}

			switch change.Type {
			case diff.UPDATE:
				groups := dstTopo.ClusterMembers[idxClusterMem].Groups
				groups[idxClusterGroups] = change.From.(string)
				clusterMemberPut.Groups = groups
				clusterMemberPuts[serverName] = clusterMemberPut
			case diff.DELETE:
				groups := dstTopo.ClusterMembers[idxClusterMem].Groups
				groups = append(groups[:idxClusterGroups], change.From.(string))
				groups = append(groups, groups[idxClusterGroups+1:]...)
				clusterMemberPut.Groups = groups
				clusterMemberPuts[serverName] = clusterMemberPut
			case diff.CREATE:
				groups := dstTopo.ClusterMembers[idxClusterMem].Groups
				groups = append(groups[:idxClusterGroups], groups[idxClusterGroups+1:]...)
				clusterMemberPut.Groups = groups
				clusterMemberPuts[serverName] = clusterMemberPut
			}
		}
	}

	// Create the cluster member edits.
	for serverName, memberPut := range clusterMemberPuts {
		alignments = append(alignments, newAlignment(fmt.Sprintf("Update cluster member %q", serverName), memberPut, func() error {
			err = dstClient.UpdateClusterMember(serverName, memberPut, "")
			if err != nil {
				return err
			}

			return nil
		}))
	}

	for serverName, newName := range clusterMemberRename {
		alignments = append(alignments, newAlignment(fmt.Sprintf("Rename cluster member %q to %q", serverName, newName), nil, func() error {
			err = dstClient.RenameClusterMember(serverName, api.ClusterMemberPost{ServerName: newName})
			if err != nil {
				return err
			}

			return nil
		}))
	}

	return alignments, diags, nil
}
