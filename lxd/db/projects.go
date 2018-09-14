package db

import (
	"fmt"

	"github.com/lxc/lxd/shared/api"
	"github.com/pkg/errors"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t projects.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -e project names
//go:generate mapper stmt -e project names-by-Name
//go:generate mapper stmt -e project objects
//go:generate mapper stmt -e project objects-by-Name
//go:generate mapper stmt -e project used-by-ref
//go:generate mapper stmt -e project used-by-ref-by-Name
//go:generate mapper stmt -e project config-ref
//go:generate mapper stmt -e project config-ref-by-Name
//go:generate mapper stmt -e project create
//go:generate mapper stmt -e project create-config-ref
//go:generate mapper stmt -e project id
//go:generate mapper stmt -e project rename
//go:generate mapper stmt -e project update
//go:generate mapper stmt -e project delete
//
//go:generate mapper method -e project URIs
//go:generate mapper method -e project List
//go:generate mapper method -e project Get
//go:generate mapper method -e project ConfigRef
//go:generate mapper method -e project Exists
//go:generate mapper method -e project Create
//go:generate mapper method -e project UsedByRef
//go:generate mapper method -e project ID
//go:generate mapper method -e project Rename
//go:generate mapper method -e project Delete

// ProjectFilter can be used to filter results yielded by ProjectList.
type ProjectFilter struct {
	Name string // If non-empty, return only the project with this name.
}

// ProjectHasProfiles is a helper to check if a project has the profiles
// feature enabled.
func (c *ClusterTx) ProjectHasProfiles(name string) (bool, error) {
	project, err := c.ProjectGet(name)
	if err != nil {
		return false, errors.Wrap(err, "fetch project")
	}

	enabled := project.Config["features.profiles"] == "true"

	return enabled, nil
}

// ProjectHasImages is a helper to check if a project has the images
// feature enabled.
func (c *ClusterTx) ProjectHasImages(name string) (bool, error) {
	project, err := c.ProjectGet(name)
	if err != nil {
		return false, errors.Wrap(err, "fetch project")
	}

	enabled := project.Config["features.images"] == "true"

	return enabled, nil
}

// ProjectUpdate updates the project matching the given key parameters.
func (c *ClusterTx) ProjectUpdate(name string, object api.ProjectPut) error {
	stmt := c.stmt(projectUpdate)
	result, err := stmt.Exec(object.Description, name)
	if err != nil {
		return errors.Wrap(err, "Update project")
	}

	n, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "Fetch affected rows")
	}
	if n != 1 {
		return fmt.Errorf("Query updated %d rows instead of 1", n)
	}

	id, err := c.ProjectID(name)
	if err != nil {
		return errors.Wrap(err, "Fetch project ID")
	}

	// Clear config.
	_, err = c.tx.Exec(`
DELETE FROM projects_config WHERE projects_config.project_id = ?
`, id)
	if err != nil {
		return errors.Wrap(err, "Delete project config")
	}

	// Insert new config.
	stmt = c.stmt(projectCreateConfigRef)
	for key, value := range object.Config {
		_, err := stmt.Exec(id, key, value)
		if err != nil {
			return errors.Wrap(err, "Insert config for project")
		}
	}

	return nil
}
