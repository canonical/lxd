package lxd

import (
	"net/http"

	"github.com/canonical/lxd/shared/api"
)

// GetReplicators returns all replicators in the given project.
func (r *ProtocolLXD) GetReplicators(project string) ([]api.Replicator, error) {
	err := r.CheckExtension("replicators")
	if err != nil {
		return nil, err
	}

	replicators := []api.Replicator{}
	u := api.NewURL().Path("replicators").Project(project).WithQuery("recursion", "1")
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", &replicators)
	if err != nil {
		return nil, err
	}

	return replicators, nil
}

// GetReplicatorNames returns a list of replicator names.
func (r *ProtocolLXD) GetReplicatorNames() ([]string, error) {
	err := r.CheckExtension("replicators")
	if err != nil {
		return nil, err
	}

	urls := []string{}
	baseURL := api.NewURL().Path("replicators").String()
	_, err = r.queryStruct(http.MethodGet, baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	return urlsToResourceNames(baseURL, urls...)
}

// GetReplicator returns a specific replicator by name and project.
func (r *ProtocolLXD) GetReplicator(project string, name string) (*api.Replicator, string, error) {
	err := r.CheckExtension("replicators")
	if err != nil {
		return nil, "", err
	}

	replicator := &api.Replicator{}
	u := api.NewURL().Path("replicators", name).Project(project)
	etag, err := r.queryStruct(http.MethodGet, u.String(), nil, "", replicator)
	if err != nil {
		return nil, "", err
	}

	return replicator, etag, nil
}

// CreateReplicator creates a new replicator.
func (r *ProtocolLXD) CreateReplicator(project string, replicator api.ReplicatorsPost) error {
	err := r.CheckExtension("replicators")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodPost, api.NewURL().Path("replicators").Project(project).String(), replicator, "")
	return err
}

// UpdateReplicator updates a replicator.
func (r *ProtocolLXD) UpdateReplicator(project string, name string, replicator api.ReplicatorPut, ETag string) error {
	err := r.CheckExtension("replicators")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodPut, api.NewURL().Path("replicators", name).Project(project).String(), replicator, ETag)
	return err
}

// DeleteReplicator deletes a replicator.
func (r *ProtocolLXD) DeleteReplicator(project string, name string) error {
	err := r.CheckExtension("replicators")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodDelete, api.NewURL().Path("replicators", name).Project(project).String(), nil, "")
	return err
}

// RunReplicator triggers a replicator run, returning the resulting bulk operation.
func (r *ProtocolLXD) RunReplicator(project string, name string, req api.ReplicatorStatePut) (Operation, error) {
	err := r.CheckExtension("replicators")
	if err != nil {
		return nil, err
	}

	op, _, err := r.queryOperation(http.MethodPut, api.NewURL().Path("replicators", name, "state").Project(project).String(), req, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetReplicatorState returns the current state of a replicator.
func (r *ProtocolLXD) GetReplicatorState(project string, name string) (*api.ReplicatorState, error) {
	err := r.CheckExtension("replicators")
	if err != nil {
		return nil, err
	}

	state := &api.ReplicatorState{}
	u := api.NewURL().Path("replicators", name, "state").Project(project)
	_, err = r.queryStruct(http.MethodGet, u.String(), nil, "", state)
	if err != nil {
		return nil, err
	}

	return state, nil
}

// RenameReplicator renames a replicator.
func (r *ProtocolLXD) RenameReplicator(project string, name string, req api.ReplicatorPost) error {
	err := r.CheckExtension("replicators")
	if err != nil {
		return err
	}

	_, _, err = r.query(http.MethodPost, api.NewURL().Path("replicators", name).Project(project).String(), req, "")
	return err
}
