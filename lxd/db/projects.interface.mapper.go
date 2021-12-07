//go:build linux && cgo && !agent
// +build linux,cgo,!agent

package db

// ProjectGenerated is an interface of generated methods for Project
type ProjectGenerated interface {
	// GetProjectConfig returns all available Project Config
	// generator: project GetMany
	GetProjectConfig(projectID int) (map[string]string, error)

	// GetProjects returns all available projects.
	// generator: project GetMany
	GetProjects(filter ProjectFilter) ([]Project, error)

	// GetProject returns the project with the given key.
	// generator: project GetOne
	GetProject(name string) (*Project, error)

	// ProjectExists checks if a project with the given key exists.
	// generator: project Exists
	ProjectExists(name string) (bool, error)

	// CreateProjectConfig adds a new project Config to the database.
	// generator: project Create
	CreateProjectConfig(projectID int64, config map[string]string) error

	// CreateProject adds a new project to the database.
	// generator: project Create
	CreateProject(object Project) (int64, error)

	// GetProjectID return the ID of the project with the given key.
	// generator: project ID
	GetProjectID(name string) (int64, error)

	// RenameProject renames the project matching the given key parameters.
	// generator: project Rename
	RenameProject(name string, to string) error

	// DeleteProject deletes the project matching the given key parameters.
	// generator: project DeleteOne-by-Name
	DeleteProject(name string) error
}
