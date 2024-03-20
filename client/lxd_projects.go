package lxd

import (
	"fmt"
	"net/url"

	"github.com/canonical/lxd/shared/api"
)

// Project handling functions

// GetProjectNames returns a list of available project names.
func (r *ProtocolLXD) GetProjectNames() ([]string, error) {
	err := r.CheckExtension("projects")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/projects"
	_, err = r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetProjects returns a list of available Project structs.
func (r *ProtocolLXD) GetProjects() ([]api.Project, error) {
	err := r.CheckExtension("projects")
	if err != nil {
		return nil, err
	}

	projects := []api.Project{}

	// Fetch the raw value
	_, err = r.queryStruct("GET", "/projects?recursion=1", nil, "", &projects)
	if err != nil {
		return nil, err
	}

	return projects, nil
}

// GetProject returns a Project entry for the provided name.
func (r *ProtocolLXD) GetProject(name string) (*api.Project, string, error) {
	err := r.CheckExtension("projects")
	if err != nil {
		return nil, "", err
	}

	project := api.Project{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/projects/%s", url.PathEscape(name)), nil, "", &project)
	if err != nil {
		return nil, "", err
	}

	return &project, etag, nil
}

// GetProjectState returns a Project state for the provided name.
func (r *ProtocolLXD) GetProjectState(name string) (*api.ProjectState, error) {
	err := r.CheckExtension("project_usage")
	if err != nil {
		return nil, err
	}

	projectState := api.ProjectState{}

	// Fetch the raw value
	_, err = r.queryStruct("GET", fmt.Sprintf("/projects/%s/state", url.PathEscape(name)), nil, "", &projectState)
	if err != nil {
		return nil, err
	}

	return &projectState, nil
}

// CreateProject defines a new container project.
func (r *ProtocolLXD) CreateProject(project api.ProjectsPost) error {
	err := r.CheckExtension("projects")
	if err != nil {
		return err
	}

	// Send the request
	_, _, err = r.query("POST", "/projects", project, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateProject updates the project to match the provided Project struct.
func (r *ProtocolLXD) UpdateProject(name string, project api.ProjectPut, ETag string) error {
	err := r.CheckExtension("projects")
	if err != nil {
		return err
	}

	// Send the request
	_, _, err = r.query("PUT", fmt.Sprintf("/projects/%s", url.PathEscape(name)), project, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameProject renames an existing project entry.
func (r *ProtocolLXD) RenameProject(name string, project api.ProjectPost) (Operation, error) {
	err := r.CheckExtension("projects")
	if err != nil {
		return nil, err
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/projects/%s", url.PathEscape(name)), project, "", true)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteProject deletes a project.
func (r *ProtocolLXD) DeleteProject(name string) error {
	err := r.CheckExtension("projects")
	if err != nil {
		return err
	}

	// Send the request
	_, _, err = r.query("DELETE", fmt.Sprintf("/projects/%s", url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
