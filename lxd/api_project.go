package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

var projectsCmd = APIEndpoint{
	Path: "projects",

	Get:  APIEndpointAction{Handler: projectsGet, AccessHandler: AllowAuthenticated},
	Post: APIEndpointAction{Handler: projectsPost},
}

var projectCmd = APIEndpoint{
	Path: "projects/{name}",

	Delete: APIEndpointAction{Handler: projectDelete},
	Get:    APIEndpointAction{Handler: projectGet, AccessHandler: AllowAuthenticated},
	Patch:  APIEndpointAction{Handler: projectPatch, AccessHandler: AllowAuthenticated},
	Post:   APIEndpointAction{Handler: projectPost},
	Put:    APIEndpointAction{Handler: projectPut, AccessHandler: AllowAuthenticated},
}

func projectsGet(d *Daemon, r *http.Request) response.Response {
	recursion := util.IsRecursionRequest(r)

	var result interface{}
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		filter := db.ProjectFilter{}
		if recursion {
			projects, err := tx.ProjectList(filter)
			if err != nil {
				return err
			}

			filtered := []api.Project{}
			for _, project := range projects {
				if !d.userHasPermission(r, project.Name, "view") {
					continue
				}

				filtered = append(filtered, project)
			}

			result = filtered
		} else {
			uris, err := tx.ProjectURIs(filter)
			if err != nil {
				return err
			}

			filtered := []string{}
			for _, uri := range uris {
				name := strings.Split(uri, "/1.0/projects/")[1]

				if !d.userHasPermission(r, name, "view") {
					continue
				}

				filtered = append(filtered, uri)
			}

			result = filtered
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, result)
}

func projectsPost(d *Daemon, r *http.Request) response.Response {
	// Parse the request
	project := api.ProjectsPost{}

	// Set default features
	if project.Config == nil {
		project.Config = map[string]string{}
	}
	for _, feature := range []string{"features.images", "features.profiles"} {
		_, ok := project.Config[feature]
		if !ok {
			project.Config[feature] = "true"
		}
	}

	err := json.NewDecoder(r.Body).Decode(&project)
	if err != nil {
		return response.BadRequest(err)
	}

	// Sanity checks
	if project.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(project.Name, "/") {
		return response.BadRequest(fmt.Errorf("Project names may not contain slashes"))
	}

	if project.Name == "*" {
		return response.BadRequest(fmt.Errorf("Reserved project name"))
	}

	if shared.StringInSlice(project.Name, []string{".", ".."}) {
		return response.BadRequest(fmt.Errorf("Invalid project name '%s'", project.Name))
	}

	// Validate the configuration
	err = projectValidateConfig(project.Config)
	if err != nil {
		return response.BadRequest(err)
	}

	var id int64
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		id, err = tx.ProjectCreate(project)
		if err != nil {
			return errors.Wrap(err, "Add project to database")
		}

		if project.Config["features.profiles"] == "true" {
			err = projectCreateDefaultProfile(tx, project.Name)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Error inserting %s into database: %s", project.Name, err))
	}

	if d.rbac != nil {
		err = d.rbac.AddProject(id, project.Name)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.SyncResponseLocation(true, nil, fmt.Sprintf("/%s/projects/%s", version.APIVersion, project.Name))
}

// Create the default profile of a project.
func projectCreateDefaultProfile(tx *db.ClusterTx, project string) error {
	// Create a default profile
	profile := db.Profile{}
	profile.Project = project
	profile.Name = "default"
	profile.Description = fmt.Sprintf("Default LXD profile for project %s", project)

	_, err := tx.ProfileCreate(profile)
	if err != nil {
		return errors.Wrap(err, "Add default profile to database")
	}
	return nil
}

func projectGet(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]

	// Check user permissions
	if !d.userHasPermission(r, name, "view") {
		return response.Forbidden(nil)
	}

	// Get the database entry
	var project *api.Project
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		project, err = tx.ProjectGet(name)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	etag := []interface{}{
		project.Description,
		project.Config["features.images"],
		project.Config["features.profiles"],
	}

	return response.SyncResponseETag(true, project, etag)
}

func projectPut(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]

	// Check user permissions
	if !d.userHasPermission(r, name, "manage-projects") {
		return response.Forbidden(nil)
	}

	// Get the current data
	var project *api.Project
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		project, err = tx.ProjectGet(name)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate ETag
	etag := []interface{}{
		project.Description,
		project.Config["features.images"],
		project.Config["features.profiles"],
	}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Parse the request
	req := api.ProjectPut{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	return projectChange(d, project, req)
}

func projectPatch(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]

	// Check user permissions
	if !d.userHasPermission(r, name, "manage-projects") {
		return response.Forbidden(nil)
	}

	// Get the current data
	var project *api.Project
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		project, err = tx.ProjectGet(name)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate ETag
	etag := []interface{}{
		project.Description,
		project.Config["features.images"],
		project.Config["features.profiles"],
	}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	rdr1 := ioutil.NopCloser(bytes.NewBuffer(body))
	rdr2 := ioutil.NopCloser(bytes.NewBuffer(body))

	reqRaw := shared.Jmap{}
	if err := json.NewDecoder(rdr1).Decode(&reqRaw); err != nil {
		return response.BadRequest(err)
	}

	req := api.ProjectPut{}
	if err := json.NewDecoder(rdr2).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	// Check what was actually set in the query
	_, err = reqRaw.GetString("description")
	if err != nil {
		req.Description = project.Description
	}

	_, err = reqRaw.GetBool("features.images")
	if err != nil {
		req.Config["features.images"] = project.Config["features.images"]
	}

	_, err = reqRaw.GetBool("features.profiles")
	if err != nil {
		req.Config["features.images"] = project.Config["features.profiles"]
	}

	return projectChange(d, project, req)
}

// Common logic between PUT and PATCH.
func projectChange(d *Daemon, project *api.Project, req api.ProjectPut) response.Response {
	// Flag indicating if any feature has changed.
	featuresChanged := req.Config["features.images"] != project.Config["features.images"] || req.Config["features.profiles"] != project.Config["features.profiles"]

	// Sanity checks
	if project.Name == "default" && featuresChanged {
		return response.BadRequest(fmt.Errorf("You can't change the features of the default project"))
	}

	if !projectIsEmpty(project) && featuresChanged {
		return response.BadRequest(fmt.Errorf("Features can only be changed on empty projects"))
	}

	// Validate the configuration
	err := projectValidateConfig(req.Config)
	if err != nil {
		return response.BadRequest(err)
	}

	// Update the database entry
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		err := tx.ProjectUpdate(project.Name, req)
		if err != nil {
			return errors.Wrap(err, "Persist profile changes")
		}

		if req.Config["features.profiles"] != project.Config["features.profiles"] {
			if req.Config["features.profiles"] == "true" {
				err = projectCreateDefaultProfile(tx, project.Name)
				if err != nil {
					return err
				}
			} else {
				// Delete the project-specific default profile.
				err = tx.ProfileDelete(project.Name, "default")
				if err != nil {
					return errors.Wrap(err, "Delete project default profile")
				}
			}
		}

		return nil

	})

	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func projectPost(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]

	// Parse the request
	req := api.ProjectPost{}

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Sanity checks
	if name == "default" {
		return response.Forbidden(fmt.Errorf("The 'default' project cannot be renamed"))
	}

	// Perform the rename
	run := func(op *operation) error {
		var id int64
		err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
			project, err := tx.ProjectGet(req.Name)
			if err != nil && err != db.ErrNoSuchObject {
				return errors.Wrapf(err, "Check if project %q exists", req.Name)
			}

			if project != nil {
				return fmt.Errorf("A project named '%s' already exists", req.Name)
			}

			project, err = tx.ProjectGet(name)
			if err != nil {
				return errors.Wrapf(err, "Fetch project %q", name)
			}

			if !projectIsEmpty(project) {
				return fmt.Errorf("Only empty projects can be renamed")
			}

			id, err = tx.ProjectID(name)
			if err != nil {
				return errors.Wrapf(err, "Fetch project id %q", name)
			}

			return tx.ProjectRename(name, req.Name)
		})
		if err != nil {
			return err
		}

		if d.rbac != nil {
			err = d.rbac.RenameProject(id, req.Name)
			if err != nil {
				return err
			}
		}

		return nil
	}

	op, err := operationCreate(d.cluster, "", operationClassTask, db.OperationProjectRename, nil, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return OperationResponse(op)
}

func projectDelete(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]

	// Sanity checks
	if name == "default" {
		return response.Forbidden(fmt.Errorf("The 'default' project cannot be deleted"))
	}

	var id int64
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		project, err := tx.ProjectGet(name)
		if err != nil {
			return errors.Wrapf(err, "Fetch project %q", name)
		}
		if !projectIsEmpty(project) {
			return fmt.Errorf("Only empty projects can be removed")
		}

		id, err = tx.ProjectID(name)
		if err != nil {
			return errors.Wrapf(err, "Fetch project id %q", name)
		}

		return tx.ProjectDelete(name)
	})

	if err != nil {
		return response.SmartError(err)
	}

	if d.rbac != nil {
		err = d.rbac.DeleteProject(id)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.EmptySyncResponse
}

// Check if a project is empty.
func projectIsEmpty(project *api.Project) bool {
	if len(project.UsedBy) > 0 {
		// Check if the only entity is the default profile.
		if len(project.UsedBy) == 1 && strings.Contains(project.UsedBy[0], "/profiles/default") {
			return true
		}
		return false
	}
	return true
}

// Validate the project configuration
var projectConfigKeys = map[string]func(value string) error{
	"features.profiles": shared.IsBool,
	"features.images":   shared.IsBool,
}

func projectValidateConfig(config map[string]string) error {
	for k, v := range config {
		key := k

		// User keys are free for all
		if strings.HasPrefix(key, "user.") {
			continue
		}

		// Then validate
		validator, ok := projectConfigKeys[key]
		if !ok {
			return fmt.Errorf("Invalid project configuration key: %s", k)
		}

		err := validator(v)
		if err != nil {
			return err
		}
	}

	return nil
}
