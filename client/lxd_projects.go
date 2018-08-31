package lxd

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/lxc/lxd/shared/api"
)

// Project handling functions

// GetProjectNames returns a list of available project names
func (r *ProtocolLXD) GetProjectNames() ([]string, error) {
	if !r.HasExtension("projects") {
		return nil, fmt.Errorf("The server is missing the required \"projects\" API extension")
	}

	urls := []string{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/projects", nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it
	names := []string{}
	for _, url := range urls {
		fields := strings.Split(url, "/projects/")
		names = append(names, fields[len(fields)-1])
	}

	return names, nil
}

// GetProjects returns a list of available Project structs
func (r *ProtocolLXD) GetProjects() ([]api.Project, error) {
	if !r.HasExtension("projects") {
		return nil, fmt.Errorf("The server is missing the required \"projects\" API extension")
	}

	projects := []api.Project{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/projects?recursion=1", nil, "", &projects)
	if err != nil {
		return nil, err
	}

	return projects, nil
}

// GetProject returns a Project entry for the provided name
func (r *ProtocolLXD) GetProject(name string) (*api.Project, string, error) {
	if !r.HasExtension("projects") {
		return nil, "", fmt.Errorf("The server is missing the required \"projects\" API extension")
	}

	project := api.Project{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/projects/%s", url.QueryEscape(name)), nil, "", &project)
	if err != nil {
		return nil, "", err
	}

	return &project, etag, nil
}

// CreateProject defines a new container project
func (r *ProtocolLXD) CreateProject(project api.ProjectsPost) error {
	if !r.HasExtension("projects") {
		return fmt.Errorf("The server is missing the required \"projects\" API extension")
	}

	// Send the request
	_, _, err := r.query("POST", "/projects", project, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateProject updates the project to match the provided Project struct
func (r *ProtocolLXD) UpdateProject(name string, project api.ProjectPut, ETag string) error {
	if !r.HasExtension("projects") {
		return fmt.Errorf("The server is missing the required \"projects\" API extension")
	}

	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/projects/%s", url.QueryEscape(name)), project, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameProject renames an existing project entry
func (r *ProtocolLXD) RenameProject(name string, project api.ProjectPost) (Operation, error) {
	if !r.HasExtension("projects") {
		return nil, fmt.Errorf("The server is missing the required \"projects\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/projects/%s", url.QueryEscape(name)), project, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteProject deletes a project
func (r *ProtocolLXD) DeleteProject(name string) error {
	if !r.HasExtension("projects") {
		return fmt.Errorf("The server is missing the required \"projects\" API extension")
	}

	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/projects/%s", url.QueryEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
