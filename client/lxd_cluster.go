package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetCluster returns information about a cluster.
func (r *ProtocolLXD) GetCluster() (*api.Cluster, string, error) {
	err := r.CheckExtension("clustering")
	if err != nil {
		return nil, "", err
	}

	cluster := &api.Cluster{}
	u := api.NewURL().Path("cluster")
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &cluster)
	if err != nil {
		return nil, "", err
	}

	return cluster, etag, nil
}

// UpdateCluster requests to bootstrap a new cluster or join an existing one.
func (r *ProtocolLXD) UpdateCluster(cluster api.ClusterPut, ETag string) (Operation, error) {
	err := r.CheckExtension("clustering")
	if err != nil {
		return nil, err
	}

	if cluster.ServerAddress != "" || cluster.ClusterToken != "" || len(cluster.MemberConfig) > 0 {
		err := r.CheckExtension("clustering_join")
		if err != nil {
			return nil, err
		}

		if cluster.ClusterToken != "" {
			err := r.CheckExtension("explicit_trust_token")
			if err != nil {
				return nil, err
			}
		}
	}

	u := api.NewURL().Path("cluster")
	op, _, err := r.queryOperation(http.MethodPut, u.String(), cluster, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteClusterMember makes the given member leave the cluster (gracefully or not,
// depending on the force flag).
func (r *ProtocolLXD) DeleteClusterMember(name string, force bool) error {
	err := r.CheckExtension("clustering")
	if err != nil {
		return err
	}

	u := api.NewURL().Path("cluster", "members", name)
	if force {
		u = u.WithQuery("force", "1")
	}

	_, _, err = r.query(http.MethodDelete, u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetClusterMemberNames returns the URLs of the current members in the cluster.
func (r *ProtocolLXD) GetClusterMemberNames() ([]string, error) {
	err := r.CheckExtension("clustering")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	u := api.NewURL().Path("cluster", "members")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(u.String(), urls...)
}

// GetClusterMembers returns the current members of the cluster.
func (r *ProtocolLXD) GetClusterMembers() ([]api.ClusterMember, error) {
	err := r.CheckExtension("clustering")
	if err != nil {
		return nil, err
	}

	members := []api.ClusterMember{}
	u := api.NewURL().Path("cluster", "members").WithQuery("recursion", "1")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &members)
	if err != nil {
		return nil, err
	}

	return members, nil
}

// GetClusterMember returns information about the given member.
func (r *ProtocolLXD) GetClusterMember(name string) (*api.ClusterMember, string, error) {
	err := r.CheckExtension("clustering")
	if err != nil {
		return nil, "", err
	}

	member := api.ClusterMember{}
	u := api.NewURL().Path("cluster", "members", name)
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &member)
	if err != nil {
		return nil, "", err
	}

	return &member, etag, nil
}

// UpdateClusterMember updates information about the given member.
func (r *ProtocolLXD) UpdateClusterMember(name string, member api.ClusterMemberPut, ETag string) error {
	err := r.CheckExtension("clustering_edit_roles")
	if err != nil {
		return err
	}

	if member.FailureDomain != "" {
		err := r.CheckExtension("clustering_failure_domains")
		if err != nil {
			return err
		}
	}

	// Send the request
	u := api.NewURL().Path("cluster", "members", name)
	_, _, err = r.query(http.MethodPut, u.String(), member, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameClusterMember changes the name of an existing member.
func (r *ProtocolLXD) RenameClusterMember(name string, member api.ClusterMemberPost) error {
	err := r.CheckExtension("clustering")
	if err != nil {
		return err
	}

	u := api.NewURL().Path("cluster", "members", name)
	_, _, err = r.query(http.MethodPost, u.String(), member, "")
	if err != nil {
		return err
	}

	return nil
}

// CreateClusterMember generates a join token to add a cluster member.
func (r *ProtocolLXD) CreateClusterMember(member api.ClusterMembersPost) (Operation, error) {
	err := r.CheckExtension("clustering_join_token")
	if err != nil {
		return nil, err
	}

	u := api.NewURL().Path("cluster", "members")
	op, _, err := r.queryOperation(http.MethodPost, u.String(), member, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateClusterCertificate updates the cluster certificate for every node in the cluster.
func (r *ProtocolLXD) UpdateClusterCertificate(certs api.ClusterCertificatePut, ETag string) error {
	err := r.CheckExtension("clustering_update_cert")
	if err != nil {
		return err
	}

	u := api.NewURL().Path("cluster", "certificate")
	_, _, err = r.query(http.MethodPut, u.String(), certs, ETag)
	if err != nil {
		return err
	}

	return nil
}

// GetClusterMemberState gets state information about a cluster member.
func (r *ProtocolLXD) GetClusterMemberState(name string) (*api.ClusterMemberState, string, error) {
	err := r.CheckExtension("cluster_member_state")
	if err != nil {
		return nil, "", err
	}

	state := api.ClusterMemberState{}
	u := api.NewURL().Path("cluster", "members", name, "state")
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &state)
	if err != nil {
		return nil, "", err
	}

	return &state, etag, err
}

// UpdateClusterMemberState evacuates or restores a cluster member.
func (r *ProtocolLXD) UpdateClusterMemberState(name string, state api.ClusterMemberStatePost) (Operation, error) {
	err := r.CheckExtension("clustering_evacuation")
	if err != nil {
		return nil, err
	}

	if state.Action != "" {
		err = r.CheckExtension("clustering_restore_skip_mode")
		if err != nil {
			return nil, err
		}
	}

	u := api.NewURL().Path("cluster", "members", name, "state")
	op, _, err := r.queryOperation(http.MethodPost, u.String(), state, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetClusterGroups returns the cluster groups.
func (r *ProtocolLXD) GetClusterGroups() ([]api.ClusterGroup, error) {
	err := r.CheckExtension("clustering_groups")
	if err != nil {
		return nil, err
	}

	groups := []api.ClusterGroup{}
	u := api.NewURL().Path("cluster", "groups").WithQuery("recursion", "1")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &groups)
	if err != nil {
		return nil, err
	}

	return groups, nil
}

// GetClusterGroupNames returns the cluster group names.
func (r *ProtocolLXD) GetClusterGroupNames() ([]string, error) {
	err := r.CheckExtension("clustering_groups")
	if err != nil {
		return nil, err
	}

	urls := []string{}
	u := api.NewURL().Path("cluster", "groups")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames("/1.0/cluster/groups", urls...)
}

// RenameClusterGroup changes the name of an existing cluster group.
func (r *ProtocolLXD) RenameClusterGroup(name string, group api.ClusterGroupPost) error {
	err := r.CheckExtension("clustering_groups")
	if err != nil {
		return err
	}

	u := api.NewURL().Path("cluster", "groups", name)
	_, _, err = r.query(http.MethodPost, u.String(), group, "")
	if err != nil {
		return err
	}

	return nil
}

// CreateClusterGroup creates a new cluster group.
func (r *ProtocolLXD) CreateClusterGroup(group api.ClusterGroupsPost) error {
	err := r.CheckExtension("clustering_groups")
	if err != nil {
		return err
	}

	u := api.NewURL().Path("cluster", "groups")
	_, _, err = r.query(http.MethodPost, u.String(), group, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteClusterGroup deletes an existing cluster group.
func (r *ProtocolLXD) DeleteClusterGroup(name string) error {
	err := r.CheckExtension("clustering_groups")
	if err != nil {
		return err
	}

	u := api.NewURL().Path("cluster", "groups", name)
	_, _, err = r.query(http.MethodDelete, u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateClusterGroup updates information about the given cluster group.
func (r *ProtocolLXD) UpdateClusterGroup(name string, group api.ClusterGroupPut, ETag string) error {
	err := r.CheckExtension("clustering_groups")
	if err != nil {
		return err
	}

	// Send the request
	u := api.NewURL().Path("cluster", "groups", name)
	_, _, err = r.query(http.MethodPut, u.String(), group, ETag)
	if err != nil {
		return err
	}

	return nil
}

// GetClusterGroup returns information about the given cluster group.
func (r *ProtocolLXD) GetClusterGroup(name string) (*api.ClusterGroup, string, error) {
	err := r.CheckExtension("clustering_groups")
	if err != nil {
		return nil, "", err
	}

	group := api.ClusterGroup{}
	u := api.NewURL().Path("cluster", "groups", name)
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", &group)
	if err != nil {
		return nil, "", err
	}

	return &group, etag, nil
}
