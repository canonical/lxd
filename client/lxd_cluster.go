package lxd

import (
	"fmt"

	"github.com/lxc/lxd/shared/api"
)

// GetCluster returns information about a cluster
//
// If this client is not trusted, the password must be supplied
func (r *ProtocolLXD) GetCluster(password string) (*api.Cluster, string, error) {
	cluster := &api.Cluster{}
	path := "/cluster"
	if password != "" {
		path += fmt.Sprintf("?password=%s", password)
	}
	etag, err := r.queryStruct("GET", path, nil, "", &cluster)
	if err != nil {
		return nil, "", err
	}

	return cluster, etag, nil
}

// BootstrapCluster requests to bootstrap a new cluster
func (r *ProtocolLXD) BootstrapCluster(name string) (*Operation, error) {
	cluster := api.ClusterPut{Name: name}
	op, _, err := r.queryOperation("POST", "/cluster/members", cluster, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// JoinCluster requests to join an existing cluster
func (r *ProtocolLXD) JoinCluster(targetAddress, targetCert, name string) (*Operation, error) {
	cluster := api.ClusterPut{
		TargetAddress: targetAddress,
		TargetCert:    targetCert,
		Name:          name,
	}
	op, _, err := r.queryOperation("POST", "/cluster/members", cluster, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteClusterMember makes the given member leave the cluster (gracefully or not,
// depending on the force flag)
func (r *ProtocolLXD) DeleteClusterMember(name string, force bool) error {
	params := ""
	if force {
		params += "?force=1"
	}
	url := fmt.Sprintf("/cluster/members/%s%s", name, params)
	_, err := r.queryStruct("DELETE", url, nil, "", nil)
	return err
}

// GetClusterMemberNames returns the URLs of the current members in the cluster
func (r *ProtocolLXD) GetClusterMemberNames() ([]string, error) {
	urls := []string{}
	path := "/cluster/members"
	_, err := r.queryStruct("GET", path, nil, "", &urls)

	if err != nil {
		return nil, err
	}

	return urls, nil
}

// GetClusterMembers returns the current members of the cluster
func (r *ProtocolLXD) GetClusterMembers() ([]api.ClusterMember, error) {
	members := []api.ClusterMember{}
	path := "/cluster/members?recursion=1"
	_, err := r.queryStruct("GET", path, nil, "", &members)

	if err != nil {
		return nil, err
	}

	return members, nil
}

// GetClusterMember returns information about the given member
func (r *ProtocolLXD) GetClusterMember(name string) (*api.ClusterMember, string, error) {
	member := api.ClusterMember{}
	path := fmt.Sprintf("/cluster/members/%s", name)
	etag, err := r.queryStruct("GET", path, nil, "", &member)

	if err != nil {
		return nil, "", err
	}

	return &member, etag, nil
}

// RenameClusterMember changes the name of an existing member
func (r *ProtocolLXD) RenameClusterMember(name string, member api.ClusterMemberPost) error {
	url := fmt.Sprintf("/cluster/members/%s", name)
	_, _, err := r.query("POST", url, member, "")
	return err
}
