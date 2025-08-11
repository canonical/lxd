//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/canonical/lxd/lxd/db/query"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t projects.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e project objects
//go:generate mapper stmt -e project objects-by-Name
//go:generate mapper stmt -e project objects-by-ID
//go:generate mapper stmt -e project create struct=Project
//go:generate mapper stmt -e project id
//go:generate mapper stmt -e project rename
//go:generate mapper stmt -e project update struct=Project
//go:generate mapper stmt -e project delete-by-Name
//
//go:generate mapper method -i -e project GetMany
//go:generate mapper method -i -e project GetOne struct=Project
//go:generate mapper method -i -e project Create references=Config
//go:generate mapper method -i -e project ID struct=Project
//go:generate mapper method -i -e project Rename
//go:generate mapper method -i -e project DeleteOne-by-Name
//go:generate goimports -w projects.mapper.go
//go:generate goimports -w projects.interface.mapper.go

// ProjectFeature indicates the behaviour of a project feature.
type ProjectFeature struct {
	// DefaultEnabled
	// Whether the feature should be enabled by default on new projects.
	DefaultEnabled bool

	// CanEnableNonEmpty
	// Whether or not the feature can be changed to enabled on a non-empty project.
	CanEnableNonEmpty bool
}

// ProjectFeatures lists available project features and their behaviours.
var ProjectFeatures = map[string]ProjectFeature{
	"features.images": {
		DefaultEnabled: true,
	},
	"features.profiles": {
		DefaultEnabled: true,
	},
	"features.storage.volumes": {
		DefaultEnabled: true,
	},
	"features.storage.buckets": {
		DefaultEnabled: true,
	},
	"features.networks": {},
	"features.networks.zones": {
		CanEnableNonEmpty: true,
	},
}

// Project represents a LXD project.
type Project struct {
	ID          int
	Description string
	Name        string `db:"omit=update"`
}

// ProjectFilter specifies potential query parameter fields.
type ProjectFilter struct {
	ID   *int
	Name *string `db:"omit=update"` // If non-empty, return only the project with this name.
}

// ToAPI converts the database Project struct to an api.Project entry.
func (p *Project) ToAPI(ctx context.Context, tx *sql.Tx) (*api.Project, error) {
	apiProject := &api.Project{
		Name:        p.Name,
		Description: p.Description,
	}

	var err error
	apiProject.Config, err = GetProjectConfig(ctx, tx, p.Name)
	if err != nil {
		return nil, fmt.Errorf("Failed loading project config: %w", err)
	}

	return apiProject, nil
}

// ProjectHasProfiles is a helper to check if a project has the profiles
// feature enabled.
func ProjectHasProfiles(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	stmt := `
SELECT projects_config.value
  FROM projects_config
  JOIN projects ON projects.id=projects_config.project_id
 WHERE projects.name=? AND projects_config.key='features.profiles'
`
	values, err := query.SelectStrings(ctx, tx, stmt, name)
	if err != nil {
		return false, fmt.Errorf("Fetch project config: %w", err)
	}

	if len(values) == 0 {
		return false, nil
	}

	return shared.IsTrue(values[0]), nil
}

// GetProjectConfig is a helper to return a config of a project.
func GetProjectConfig(ctx context.Context, tx *sql.Tx, projectName string) (map[string]string, error) {
	stmt := `
	SELECT projects_config.key, projects_config.value
	  FROM projects_config
	  JOIN projects ON projects.id=projects_config.project_id
	 WHERE projects.name=?
	`

	result := make(map[string]string)
	err := query.Scan(ctx, tx, stmt, func(scan func(dest ...any) error) error {
		var key, value string
		err := scan(&key, &value)
		if err != nil {
			return err
		}

		result[key] = value
		return nil
	}, projectName)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// GetProjectNames returns the names of all availablprojects.
func GetProjectNames(ctx context.Context, tx *sql.Tx) ([]string, error) {
	stmt := "SELECT name FROM projects"

	names, err := query.SelectStrings(ctx, tx, stmt)
	if err != nil {
		return nil, fmt.Errorf("Fetch project names: %w", err)
	}

	return names, nil
}

// GetProjectIDsToNames returns a map associating each prect ID to its
// project name.
func GetProjectIDsToNames(ctx context.Context, tx *sql.Tx) (map[int64]string, error) {
	stmt := "SELECT id, name FROM projects"

	rows, err := tx.QueryContext(ctx, stmt)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	result := map[int64]string{}
	for i := 0; rows.Next(); i++ {
		var id int64
		var name string

		err := rows.Scan(&id, &name)
		if err != nil {
			return nil, err
		}

		result[id] = name
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	return result, nil
}

// ProjectHasImages is a helper to check if a project has the images
// feature enabled.
func ProjectHasImages(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	config, err := GetProjectConfig(ctx, tx, name)
	if err != nil {
		return false, err
	}

	enabled := shared.IsTrue(config["features.images"])

	return enabled, nil
}

// UpdateProject updates the project matching the given key parameters.
func UpdateProject(ctx context.Context, tx *sql.Tx, name string, object api.ProjectPut) error {
	id, err := GetProjectID(ctx, tx, name)
	if err != nil {
		return fmt.Errorf("Fetch project ID: %w", err)
	}

	stmt, err := Stmt(tx, projectUpdate)
	if err != nil {
		return fmt.Errorf("Failed to get \"projectUpdate\" prepared statement: %w", err)
	}

	result, err := stmt.Exec(object.Description, id)
	if err != nil {
		return fmt.Errorf("Update project: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("Fetch affected rows: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("Query updated %d rows instead of 1", n)
	}

	// Clear config.
	_, err = tx.Exec(`
DELETE FROM projects_config WHERE projects_config.project_id = ?
`, id)
	if err != nil {
		return fmt.Errorf("Delete project config: %w", err)
	}

	err = UpdateConfig(ctx, tx, "project", int(id), object.Config)
	if err != nil {
		return fmt.Errorf("Insert config for project: %w", err)
	}

	return nil
}

// InitProjectWithoutImages populates the images_profiles table with
// all images from the default project when a project is created with
// features.images=false.
func InitProjectWithoutImages(ctx context.Context, tx *sql.Tx, project string) error {
	defaultProfileID, err := GetProfileID(ctx, tx, project, "default")
	if err != nil {
		return fmt.Errorf("Fetch project ID: %w", err)
	}

	stmt := `INSERT INTO images_profiles (image_id, profile_id)
	SELECT images.id, ? FROM images WHERE project_id=1`
	_, err = tx.Exec(stmt, defaultProfileID)
	return err
}

// GetAllProjectsConfig returns a map of project name to config map.
func GetAllProjectsConfig(ctx context.Context, tx *sql.Tx) (map[string]map[string]string, error) {
	projectConfigs := make(map[string]map[string]string)
	err := query.Scan(ctx, tx, "SELECT projects.name, projects_config.key, projects_config.value FROM projects JOIN projects_config ON projects.id = projects_config.project_id", func(scan func(dest ...any) error) error {
		var projectName, key, value string
		err := scan(&projectName, &key, &value)
		if err != nil {
			return err
		}

		projectConfig, ok := projectConfigs[projectName]
		if !ok {
			projectConfigs[projectName] = map[string]string{key: value}
			return nil
		}

		projectConfig[key] = value
		return nil
	})
	if err != nil {
		return nil, err
	}

	return projectConfigs, nil
}
