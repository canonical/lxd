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
	"github.com/lxc/lxd/lxd/operations"
	projecthelpers "github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

var projectFeatures = []string{"features.images", "features.profiles", "features.storage.volumes"}

var projectsCmd = APIEndpoint{
	Path: "projects",

	Get:  APIEndpointAction{Handler: projectsGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: projectsPost},
}

var projectCmd = APIEndpoint{
	Path: "projects/{name}",

	Delete: APIEndpointAction{Handler: projectDelete},
	Get:    APIEndpointAction{Handler: projectGet, AccessHandler: allowAuthenticated},
	Patch:  APIEndpointAction{Handler: projectPatch, AccessHandler: allowAuthenticated},
	Post:   APIEndpointAction{Handler: projectPost},
	Put:    APIEndpointAction{Handler: projectPut, AccessHandler: allowAuthenticated},
}

func projectsGet(d *Daemon, r *http.Request) response.Response {
	recursion := util.IsRecursionRequest(r)

	var result interface{}
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		filter := db.ProjectFilter{}
		if recursion {
			projects, err := tx.GetProjects(filter)
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
			uris, err := tx.GetProjectURIs(filter)
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
	for _, feature := range projectFeatures {
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
	err = projectValidateName(project.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate the configuration
	err = projectValidateConfig(project.Config)
	if err != nil {
		return response.BadRequest(err)
	}

	var id int64
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		id, err = tx.CreateProject(project)
		if err != nil {
			return errors.Wrap(err, "Add project to database")
		}

		if shared.IsTrue(project.Config["features.profiles"]) {
			err = projectCreateDefaultProfile(tx, project.Name)
			if err != nil {
				return err
			}

			if project.Config["features.images"] == "false" {
				err = tx.InitProjectWithoutImages(project.Name)
				if err != nil {
					return err
				}
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
	profile.Name = projecthelpers.Default
	profile.Description = fmt.Sprintf("Default LXD profile for project %s", project)

	_, err := tx.CreateProfile(profile)
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
		project, err = tx.GetProject(name)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	etag := []interface{}{
		project.Description,
		project.Config,
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
		project, err = tx.GetProject(name)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate ETag
	etag := []interface{}{
		project.Description,
		project.Config,
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
		project, err = tx.GetProject(name)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate ETag
	etag := []interface{}{
		project.Description,
		project.Config,
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

	config, err := reqRaw.GetMap("config")
	if err != nil {
		req.Config = project.Config
	} else {
		for k, v := range project.Config {
			_, ok := config[k]
			if !ok {
				config[k] = v
			}
		}
	}

	return projectChange(d, project, req)
}

// Common logic between PUT and PATCH.
func projectChange(d *Daemon, project *api.Project, req api.ProjectPut) response.Response {
	// Make a list of config keys that have changed.
	configChanged := []string{}
	for key := range project.Config {
		if req.Config[key] != project.Config[key] {
			configChanged = append(configChanged, key)
		}
	}

	for key := range req.Config {
		_, ok := project.Config[key]
		if !ok {
			configChanged = append(configChanged, key)
		}
	}

	// Flag indicating if any feature has changed.
	featuresChanged := false
	for _, featureKey := range projectFeatures {
		if shared.StringInSlice(featureKey, configChanged) {
			featuresChanged = true
			break
		}
	}

	// Sanity checks.
	if project.Name == projecthelpers.Default && featuresChanged {
		return response.BadRequest(fmt.Errorf("You can't change the features of the default project"))
	}

	if !projectIsEmpty(project) && featuresChanged {
		return response.BadRequest(fmt.Errorf("Features can only be changed on empty projects"))
	}

	// Validate the configuration.
	err := projectValidateConfig(req.Config)
	if err != nil {
		return response.BadRequest(err)
	}

	// Update the database entry.
	err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
		err := projecthelpers.AllowProjectUpdate(tx, project.Name, req.Config, configChanged)
		if err != nil {
			return err
		}

		err = tx.UpdateProject(project.Name, req)
		if err != nil {
			return errors.Wrap(err, "Persist profile changes")
		}

		if shared.StringInSlice("features.profiles", configChanged) {
			if shared.IsTrue(req.Config["features.profiles"]) {
				err = projectCreateDefaultProfile(tx, project.Name)
				if err != nil {
					return err
				}
			} else {
				// Delete the project-specific default profile.
				err = tx.DeleteProfile(project.Name, projecthelpers.Default)
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
	if name == projecthelpers.Default {
		return response.Forbidden(fmt.Errorf("The 'default' project cannot be renamed"))
	}

	// Perform the rename
	run := func(op *operations.Operation) error {
		var id int64
		err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
			project, err := tx.GetProject(req.Name)
			if err != nil && err != db.ErrNoSuchObject {
				return errors.Wrapf(err, "Check if project %q exists", req.Name)
			}

			if project != nil {
				return fmt.Errorf("A project named '%s' already exists", req.Name)
			}

			project, err = tx.GetProject(name)
			if err != nil {
				return errors.Wrapf(err, "Fetch project %q", name)
			}

			if !projectIsEmpty(project) {
				return fmt.Errorf("Only empty projects can be renamed")
			}

			id, err = tx.GetProjectID(name)
			if err != nil {
				return errors.Wrapf(err, "Fetch project id %q", name)
			}

			err = projectValidateName(name)
			if err != nil {
				return err
			}

			return tx.RenameProject(name, req.Name)
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

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationProjectRename, nil, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func projectDelete(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]

	// Sanity checks
	if name == projecthelpers.Default {
		return response.Forbidden(fmt.Errorf("The 'default' project cannot be deleted"))
	}

	var id int64
	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		project, err := tx.GetProject(name)
		if err != nil {
			return errors.Wrapf(err, "Fetch project %q", name)
		}
		if !projectIsEmpty(project) {
			return fmt.Errorf("Only empty projects can be removed")
		}

		id, err = tx.GetProjectID(name)
		if err != nil {
			return errors.Wrapf(err, "Fetch project id %q", name)
		}

		return tx.DeleteProject(name)
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

func isEitherAllowOrBlock(value string) error {
	return shared.IsOneOf(value, []string{"block", "allow"})
}

func isEitherAllowOrBlockOrManaged(value string) error {
	return shared.IsOneOf(value, []string{"block", "allow", "managed"})
}

// Validate the project configuration
var projectConfigKeys = map[string]func(value string) error{
	"features.profiles":              shared.IsBool,
	"features.images":                shared.IsBool,
	"features.storage.volumes":       shared.IsBool,
	"limits.containers":              shared.IsUint32,
	"limits.virtual-machines":        shared.IsUint32,
	"limits.memory":                  shared.IsSize,
	"limits.processes":               shared.IsUint32,
	"limits.cpu":                     shared.IsUint32,
	"limits.disk":                    shared.IsSize,
	"restricted":                     shared.IsBool,
	"restricted.containers.nesting":  isEitherAllowOrBlock,
	"restricted.containers.lowlevel": isEitherAllowOrBlock,
	"restricted.containers.privilege": func(value string) error {
		return shared.IsOneOf(value, []string{"allow", "unprivileged", "isolated"})
	},
	"restricted.virtual-machines.lowlevel": isEitherAllowOrBlock,
	"restricted.devices.unix-char":         isEitherAllowOrBlock,
	"restricted.devices.unix-block":        isEitherAllowOrBlock,
	"restricted.devices.unix-hotplug":      isEitherAllowOrBlock,
	"restricted.devices.infiniband":        isEitherAllowOrBlock,
	"restricted.devices.gpu":               isEitherAllowOrBlock,
	"restricted.devices.usb":               isEitherAllowOrBlock,
	"restricted.devices.nic":               isEitherAllowOrBlockOrManaged,
	"restricted.devices.disk":              isEitherAllowOrBlockOrManaged,
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

func projectValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("No name provided")
	}

	if strings.Contains(name, "/") {
		return fmt.Errorf("Project names may not contain slashes")
	}

	if strings.Contains(name, " ") {
		return fmt.Errorf("Project names may not contain spaces")
	}

	if name == "*" {
		return fmt.Errorf("Reserved project name")
	}

	if shared.StringInSlice(name, []string{".", ".."}) {
		return fmt.Errorf("Invalid project name '%s'", name)
	}

	return nil
}
