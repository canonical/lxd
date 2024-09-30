package nodes

import (
	"errors"
	"fmt"
	"sort"

	lxd "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
	"github.com/r3labs/diff/v3"
)

// RootNode contains:
// - The `cluster` config keys
// - The `global scope` server config keys
type RootNode struct {
	baseNode
}

func (r *RootNode) Diff(n any) (diff.Changelog, error) {
	other, ok := n.(*RootNode)
	if !ok {
		return nil, errors.New("Failed to cast target node to RootNode")
	}

	srcData, ok := r.data.(RootData)
	if !ok {
		return nil, errors.New("Failed to cast data to RootData")
	}

	dstData, ok := other.data.(RootData)
	if !ok {
		return nil, errors.New("Failed to cast data to RootData")
	}

	// Creating a differ with the struct values disabled helps to directly see if we don't have the same number of high level struct
	// in the arrays (like ClusterGroups, ClusterMembers, ServerConfigs). We can easily see if the number of cluster member differs which
	// is a critical change. Else, we'd have to go through all the diff from an inner struct which is not very helpful nor efficient.
	differ, err := diff.NewDiffer(diff.DisableStructValues())
	if err != nil {
		return nil, fmt.Errorf("Failed to create differ: %w", err)
	}

	return differ.Diff(srcData, dstData)
}

// RootData represents the data of the RootNode.
type RootData struct {
	ClusterGroups  []api.ClusterGroup  `json:"cluster_groups" diff:"cluster_groups"`
	ClusterMembers []api.ClusterMember `json:"cluster_members" diff:"cluster_members"`
	ServerConfigs  []api.Server        `json:"server_configs" diff:"server_configs"`
}

func NewRootNode(client lxd.InstanceServer, id int64) (*RootNode, error) {
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

	data := RootData{
		ClusterGroups:  clusterGroups,
		ClusterMembers: clusterMembers,
		ServerConfigs:  serverConfigs,
	}

	return &RootNode{
		baseNode: baseNode{
			data:    data,
			id:      id,
			humanID: "root",
		},
	}, nil
}

func NewRootNodeFromData(data RootData, id int64) *RootNode {
	return &RootNode{
		baseNode: baseNode{
			data:    data,
			id:      id,
			humanID: "root",
		},
	}
}
