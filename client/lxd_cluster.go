package lxd

import (
	"fmt"

	"github.com/lxc/lxd/shared/api"
)

// GetCluster returns information about a cluster
//
// If this client is not trusted, the password must be supplied
func (r *ProtocolLXD) GetCluster() (*api.Cluster, string, error) {
	if !r.HasExtension("clustering") {
		return nil, "", fmt.Errorf("The server is missing the required \"clustering\" API extension")
	}

	cluster := &api.Cluster{}
	etag, err := r.queryStruct("GET", "/cluster", nil, "", &cluster)
	if err != nil {
		return nil, "", err
	}

	return cluster, etag, nil
}

// UpdateCluster requests to bootstrap a new cluster
func (r *ProtocolLXD) UpdateCluster(cluster api.ClusterPut, ETag string) (Operation, error) {
	if !r.HasExtension("clustering") {
		return nil, fmt.Errorf("The server is missing the required \"clustering\" API extension")
	}

	if cluster.ServerAddress != "" || cluster.ClusterPassword != "" || len(cluster.MemberConfig) > 0 {
		if !r.HasExtension("clustering_join") {
			return nil, fmt.Errorf("The server is missing the required \"clustering_join\" API extension")
		}
	}

	op, _, err := r.queryOperation("PUT", "/cluster", cluster, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteClusterMember makes the given member leave the cluster (gracefully or not,
// depending on the force flag)
func (r *ProtocolLXD) DeleteClusterMember(name string, force bool) error {
	if !r.HasExtension("clustering") {
		return fmt.Errorf("The server is missing the required \"clustering\" API extension")
	}

	params := ""
	if force {
		params += "?force=1"
	}

	_, err := r.queryStruct("DELETE", fmt.Sprintf("/cluster/members/%s%s", name, params), nil, "", nil)
	if err != nil {
		return err
	}

	return nil
}

// GetClusterMemberNames returns the URLs of the current members in the cluster
func (r *ProtocolLXD) GetClusterMemberNames() ([]string, error) {
	if !r.HasExtension("clustering") {
		return nil, fmt.Errorf("The server is missing the required \"clustering\" API extension")
	}

	urls := []string{}
	_, err := r.queryStruct("GET", "/cluster/members", nil, "", &urls)
	if err != nil {
		return nil, err
	}

	return urls, nil
}

// GetClusterMembers returns the current members of the cluster
func (r *ProtocolLXD) GetClusterMembers() ([]api.ClusterMember, error) {
	if !r.HasExtension("clustering") {
		return nil, fmt.Errorf("The server is missing the required \"clustering\" API extension")
	}

	members := []api.ClusterMember{}
	_, err := r.queryStruct("GET", "/cluster/members?recursion=1", nil, "", &members)
	if err != nil {
		return nil, err
	}

	return members, nil
}

// GetClusterMember returns information about the given member
func (r *ProtocolLXD) GetClusterMember(name string) (*api.ClusterMember, string, error) {
	if !r.HasExtension("clustering") {
		return nil, "", fmt.Errorf("The server is missing the required \"clustering\" API extension")
	}

	member := api.ClusterMember{}
	etag, err := r.queryStruct("GET", fmt.Sprintf("/cluster/members/%s", name), nil, "", &member)
	if err != nil {
		return nil, "", err
	}

	return &member, etag, nil
}

// RenameClusterMember changes the name of an existing member
func (r *ProtocolLXD) RenameClusterMember(name string, member api.ClusterMemberPost) error {
	if !r.HasExtension("clustering") {
		return fmt.Errorf("The server is missing the required \"clustering\" API extension")
	}

	_, _, err := r.query("POST", fmt.Sprintf("/cluster/members/%s", name), member, "")
	if err != nil {
		return err
	}

	return nil
}
