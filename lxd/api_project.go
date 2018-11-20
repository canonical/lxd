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
	"github.com/lxc/lxd/lxd/types"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

var projectsCmd = Command{
	name: "projects",
	get:  apiProjectsGet,
	post: apiProjectsPost,
}

var projectCmd = Command{
	name:   "projects/{name}",
	get:    apiProjectGet,
	post:   apiProjectPost,
	put:    apiProjectPut,
	patch:  apiProjectPatch,
	delete: apiProjectDelete,
}

func apiProjectsGet(d *Daemon, r *http.Request) Response {
	recursion := util.IsRecursionRequest(r)

	var result interface{}
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		filter := db.ProjectFilter{}
		if recursion {
			result, err = tx.ProjectList(filter)
		} else {
			result, err = tx.ProjectURIs(filter)
		}
		return err
	})
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, result)
}

func apiProjectsPost(d *Daemon, r *http.Request) Response {
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
		return BadRequest(err)
	}

	// Sanity checks
	if project.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(project.Name, "/") {
		return BadRequest(fmt.Errorf("Project names may not contain slashes"))
	}

	if project.Name == "*" {
		return BadRequest(fmt.Errorf("Reserved project name"))
	}

	if shared.StringInSlice(project.Name, []string{".", ".."}) {
		return BadRequest(fmt.Errorf("Invalid project name '%s'", project.Name))
	}

	// Validate the configuration
	err = projectValidateConfig(project.Config)
	if err != nil {
		return BadRequest(err)
	}

	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		_, err := tx.ProjectCreate(project)
		if err != nil {
			return errors.Wrap(err, "Add project to database")
		}

		if project.Config["features.profiles"] == "true" {
			err = apiProjectCreateDefaultProfile(tx, project.Name)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return SmartError(fmt.Errorf("Error inserting %s into database: %s", project.Name, err))
	}

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/projects/%s", version.APIVersion, project.Name))
}

// Create the default profile of a project.
func apiProjectCreateDefaultProfile(tx *db.ClusterTx, project string) error {
	// Create a default profile
	profile := db.Profile{}
	profile.Project = project
	profile.Name = "default"
	profile.Description = fmt.Sprintf("Default LXD profile for project %s", project)
	profile.Config = map[string]string{}
	profile.Devices = types.Devices{}

	_, err := tx.ProfileCreate(profile)
	if err != nil {
		return errors.Wrap(err, "Add default profile to database")
	}
	return nil
}

func apiProjectGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	// Get the database entry
	var project *api.Project
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		project, err = tx.ProjectGet(name)
		return err
	})
	if err != nil {
		return SmartError(err)
	}

	etag := []interface{}{
		project.Description,
		project.Config["features.images"],
		project.Config["features.profiles"],
	}

	return SyncResponseETag(true, project, etag)
}

func apiProjectPut(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	// Get the current data
	var project *api.Project
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		project, err = tx.ProjectGet(name)
		return err
	})
	if err != nil {
		return SmartError(err)
	}

	// Validate ETag
	etag := []interface{}{
		project.Description,
		project.Config["features.images"],
		project.Config["features.profiles"],
	}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	// Parse the request
	req := api.ProjectPut{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	return apiProjectChange(d, project, req)
}

func apiProjectPatch(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	// Get the current data
	var project *api.Project
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error
		project, err = tx.ProjectGet(name)
		return err
	})
	if err != nil {
		return SmartError(err)
	}

	// Validate ETag
	etag := []interface{}{
		project.Description,
		project.Config["features.images"],
		project.Config["features.profiles"],
	}
	err = util.EtagCheck(r, etag)
	if err != nil {
		return PreconditionFailed(err)
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return InternalError(err)
	}

	rdr1 := ioutil.NopCloser(bytes.NewBuffer(body))
	rdr2 := ioutil.NopCloser(bytes.NewBuffer(body))

	reqRaw := shared.Jmap{}
	if err := json.NewDecoder(rdr1).Decode(&reqRaw); err != nil {
		return BadRequest(err)
	}

	req := api.ProjectPut{}
	if err := json.NewDecoder(rdr2).Decode(&req); err != nil {
		return BadRequest(err)
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

	return apiProjectChange(d, project, req)
}

// Common logic between PUT and PATCH.
func apiProjectChange(d *Daemon, project *api.Project, req api.ProjectPut) Response {
	// Flag indicating if any feature has changed.
	featuresChanged := req.Config["features.images"] != project.Config["features.images"] || req.Config["features.profiles"] != project.Config["features.profiles"]

	// Sanity checks
	if project.Name == "default" && featuresChanged {
		return BadRequest(fmt.Errorf("You can't change the features of the default project"))
	}

	if !apiProjectIsEmpty(project) && featuresChanged {
		return BadRequest(fmt.Errorf("Features can only be changed on empty projects"))
	}

	// Validate the configuration
	err := projectValidateConfig(req.Config)
	if err != nil {
		return BadRequest(err)
	}

	// Update the database entry
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		err := tx.ProjectUpdate(project.Name, req)
		if err != nil {
			return errors.Wrap(err, "Persist profile changes")
		}

		if req.Config["features.profiles"] != project.Config["features.profiles"] {
			if req.Config["features.profiles"] == "true" {
				err = apiProjectCreateDefaultProfile(tx, project.Name)
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
		return SmartError(err)
	}

	return EmptySyncResponse
}

func apiProjectPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	// Parse the request
	req := api.ProjectPost{}

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks
	if name == "default" {
		return Forbidden(fmt.Errorf("The 'default' project cannot be renamed"))
	}

	// Perform the rename
	run := func(op *operation) error {
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

			if !apiProjectIsEmpty(project) {
				return fmt.Errorf("Only empty projects can be renamed")
			}

			return tx.ProjectRename(name, req.Name)
		})

		return err
	}

	op, err := operationCreate(d.cluster, "", operationClassTask, db.OperationProjectRename, nil, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func apiProjectDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	// Sanity checks
	if name == "default" {
		return Forbidden(fmt.Errorf("The 'default' project cannot be deleted"))
	}

	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		project, err := tx.ProjectGet(name)
		if err != nil {
			return errors.Wrapf(err, "Fetch project %q", name)
		}
		if !apiProjectIsEmpty(project) {
			return fmt.Errorf("Only empty projects can be removed")
		}

		return tx.ProjectDelete(name)
	})

	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

// Check if a project is empty.
func apiProjectIsEmpty(project *api.Project) bool {
	if len(project.UsedBy) > 0 {
		// Check if the only entity is the default profile.
		if len(project.UsedBy) == 1 && strings.Contains(project.UsedBy[0], "/profiles/default") {
			return true
		}
		return false
	}
	return true
}

// Add the "<project>_" prefix when the given project name is not "default".
func projectPrefix(project string, s string) string {
	if project != "default" {
		s = fmt.Sprintf("%s_%s", project, s)
	}
	return s
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
