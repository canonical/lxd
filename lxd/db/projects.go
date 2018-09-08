package db

import (
	"database/sql"
	"fmt"

	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t projects.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -e project names
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
//go:generate mapper method -e project Update
//go:generate mapper method -e project Delete

// ProjectFilter can be used to filter results yielded by ProjectList.
type ProjectFilter struct {
	Name string // If non-empty, return only the project with this name.
}

// Projects returns a string list of projects
func (c *Cluster) Projects() ([]api.Project, error) {
	var id int64
	var name string
	var description string
	var hasImages int
	var hasProfiles int

	// Sort out UsedBy
	usedBy := map[int64][]string{}

	// Containers
	q := fmt.Sprintf("SELECT project_id, name FROM containers WHERE type=0")
	inargs := []interface{}{}
	outfmt := []interface{}{id, name}
	result, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	for _, r := range result {
		if usedBy[r[0].(int64)] == nil {
			usedBy[r[0].(int64)] = []string{}
		}
		usedBy[r[0].(int64)] = append(usedBy[r[0].(int64)], fmt.Sprintf("/%s/containers/%s", version.APIVersion, r[1].(string)))
	}

	// Images
	q = fmt.Sprintf("SELECT project_id, fingerprint FROM images")
	result, err = queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	for _, r := range result {
		if usedBy[r[0].(int64)] == nil {
			usedBy[r[0].(int64)] = []string{}
		}
		usedBy[r[0].(int64)] = append(usedBy[r[0].(int64)], fmt.Sprintf("/%s/images/%s", version.APIVersion, r[1].(string)))
	}

	// Profiles
	q = fmt.Sprintf("SELECT project_id, name FROM profiles")
	result, err = queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	for _, r := range result {
		if usedBy[r[0].(int64)] == nil {
			usedBy[r[0].(int64)] = []string{}
		}
		usedBy[r[0].(int64)] = append(usedBy[r[0].(int64)], fmt.Sprintf("/%s/profiles/%s", version.APIVersion, r[1].(string)))
	}

	// Get the projects themselves
	q = fmt.Sprintf("SELECT id, name, description, has_images, has_profiles FROM projects")
	outfmt = []interface{}{id, name, description, hasImages, hasProfiles}
	result, err = queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return nil, err
	}

	response := []api.Project{}
	for _, r := range result {
		project := api.Project{
			Name: r[1].(string),
		}
		project.Description = r[2].(string)
		project.HasImages = r[3].(int) == 1
		project.HasProfiles = r[4].(int) == 1

		// Fill in UsedBy
		entries, ok := usedBy[r[0].(int64)]
		if ok {
			project.UsedBy = entries
		} else {
			project.UsedBy = []string{}
		}

		response = append(response, project)
	}

	return response, nil
}

// ProjectNames returns a string list of project names
func (c *Cluster) ProjectNames() ([]string, error) {
	q := fmt.Sprintf("SELECT name FROM projects")
	inargs := []interface{}{}
	var name string
	outfmt := []interface{}{name}
	result, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	response := []string{}
	for _, r := range result {
		response = append(response, r[0].(string))
	}

	return response, nil
}

// ProjectGet returns the project with the given name
func (c *Cluster) ProjectGet(name string) (int64, *api.Project, error) {
	id := int64(-1)
	hasImages := int64(-1)
	hasProfiles := int64(-1)
	description := sql.NullString{}

	q := "SELECT id, has_images, has_profiles, description FROM projects WHERE name=?"
	arg1 := []interface{}{name}
	arg2 := []interface{}{&id, &hasImages, &hasProfiles, &description}
	err := dbQueryRowScan(c.db, q, arg1, arg2)
	if err != nil {
		if err == sql.ErrNoRows {
			return -1, nil, ErrNoSuchObject
		}

		return -1, nil, err
	}

	project := api.Project{
		Name: name,
	}
	project.Description = description.String
	project.HasImages = hasImages == 1
	project.HasProfiles = hasProfiles == 1

	// Fill UsedBy
	project.UsedBy = []string{}
	var qName string
	inargs := []interface{}{id}
	outfmt := []interface{}{qName}

	// Containers
	q = fmt.Sprintf("SELECT name FROM containers WHERE project_id=? AND type=0")
	result, err := queryScan(c.db, q, inargs, outfmt)
	if err != nil {
		return -1, nil, err
	}

	for _, r := range result {
		project.UsedBy = append(project.UsedBy, fmt.Sprintf("/%s/containers/%s", version.APIVersion, r[0].(string)))
	}

	// Images
	if hasImages == 1 {
		q = fmt.Sprintf("SELECT fingerprint FROM images WHERE project_id=?")
		result, err := queryScan(c.db, q, inargs, outfmt)
		if err != nil {
			return -1, nil, err
		}

		for _, r := range result {
			project.UsedBy = append(project.UsedBy, fmt.Sprintf("/%s/images/%s", version.APIVersion, r[0].(string)))
		}
	}

	// Profiles
	if hasProfiles == 1 {
		q = fmt.Sprintf("SELECT name FROM profiles WHERE project_id=?")
		result, err := queryScan(c.db, q, inargs, outfmt)
		if err != nil {
			return -1, nil, err
		}

		for _, r := range result {
			project.UsedBy = append(project.UsedBy, fmt.Sprintf("/%s/profiles/%s", version.APIVersion, r[0].(string)))
		}
	}

	return id, &project, nil
}

// ProjectCreate creates a new project
func (c *Cluster) ProjectCreate(project api.ProjectsPost) (int64, error) {
	// Convert to integers
	hasImages := 0
	if project.HasImages {
		hasImages = 1
	}

	hasProfiles := 0
	if project.HasProfiles {
		hasProfiles = 1
	}

	// Create the database record
	var id int64
	err := c.Transaction(func(tx *ClusterTx) error {
		result, err := tx.tx.Exec("INSERT INTO projects (name, has_images, has_profiles, description) VALUES (?, ?, ?, ?)",
			project.Name, hasImages, hasProfiles, project.Description)
		if err != nil {
			return err
		}

		id, err = result.LastInsertId()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		id = -1
	}

	return id, nil
}

// ProjectCreateDefault creates the default project
func (c *Cluster) ProjectCreateDefault() error {
	id, _, _ := c.ProjectGet("default")

	if id != -1 {
		// The default project already exists
		return nil
	}

	defaultProject := api.ProjectsPost{
		Name: "default",
	}
	defaultProject.Description = "Default LXD project"
	defaultProject.HasImages = true
	defaultProject.HasProfiles = true

	_, err := c.ProjectCreate(defaultProject)
	if err != nil {
		return err
	}

	return nil
}

// ProjectDelete deletes the project with the given name
func (c *Cluster) ProjectDelete(name string) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		_, err := tx.tx.Exec("DELETE FROM projects WHERE name=?", name)
		return err
	})
	if err != nil {
		return err
	}

	return nil
}

// ProjectRename renames the project with the given name to the given new name
func (c *Cluster) ProjectRename(name string, project api.ProjectPost) error {
	err := c.Transaction(func(tx *ClusterTx) error {
		_, err := tx.tx.Exec("UPDATE projects SET name=? WHERE name=?", project.Name, name)
		return err
	})
	if err != nil {
		return err
	}

	return nil
}

// ProjectUpdate updates the various fields of a project
func (c *Cluster) ProjectUpdate(name string, project api.ProjectPut) error {
	// Convert to integers
	hasImages := 0
	if project.HasImages {
		hasImages = 1
	}

	hasProfiles := 0
	if project.HasProfiles {
		hasProfiles = 1
	}

	// Update the record
	err := c.Transaction(func(tx *ClusterTx) error {
		_, err := tx.tx.Exec("UPDATE projects SET description=?, has_images=?, has_profiles=? WHERE name=?", project.Description, hasImages, hasProfiles, name)
		return err
	})
	if err != nil {
		return err
	}

	return nil
}
