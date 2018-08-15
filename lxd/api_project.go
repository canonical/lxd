package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/db"
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

	// Recursive query
	if recursion {
		projects, err := d.cluster.Projects()
		if err != nil {
			return SmartError(err)
		}

		return SyncResponse(true, projects)
	}

	// List of URLs
	projects, err := d.cluster.ProjectNames()
	if err != nil {
		return SmartError(err)
	}

	urls := []string{}
	for _, name := range projects {
		urls = append(urls, fmt.Sprintf("/%s/projects/%s", version.APIVersion, name))
	}

	return SyncResponse(true, urls)
}

func apiProjectsPost(d *Daemon, r *http.Request) Response {
	// Parse the request
	project := api.ProjectsPost{}

	err := json.NewDecoder(r.Body).Decode(&project)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks
	if project.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	_, entry, _ := d.cluster.ProjectGet(project.Name)
	if entry != nil {
		return BadRequest(fmt.Errorf("The project already exists"))
	}

	if strings.Contains(project.Name, "/") {
		return BadRequest(fmt.Errorf("Project names may not contain slashes"))
	}

	if shared.StringInSlice(project.Name, []string{".", ".."}) {
		return BadRequest(fmt.Errorf("Invalid project name '%s'", project.Name))
	}

	// Fill the features if not set by the client
	if project.Features == nil {
		project.Features = &api.ProjectFeatures{
			Images:   true,
			Profiles: true,
		}
	}

	// Create the database entry
	_, err = d.cluster.ProjectCreate(project)
	if err != nil {
		return SmartError(fmt.Errorf("Error inserting %s into database: %s", project.Name, err))
	}

	return SyncResponseLocation(true, nil, fmt.Sprintf("/%s/projects/%s", version.APIVersion, project.Name))
}

func apiProjectGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	// Get the database entry
	_, project, err := d.cluster.ProjectGet(name)
	if err != nil {
		return SmartError(err)
	}

	etag := []interface{}{project.Description, *project.Features}
	return SyncResponseETag(true, project, etag)
}

func apiProjectPut(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	// Get the current data
	_, project, err := d.cluster.ProjectGet(name)
	if err != nil {
		return SmartError(err)
	}

	// Validate ETag
	etag := []interface{}{project.Description, *project.Features}
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

	// Sanity checks
	if project.Name == "default" && !reflect.DeepEqual(req.Features, project.Features) {
		return BadRequest(fmt.Errorf("You can't change the features of the default project"))
	}

	if len(project.UsedBy) != 0 && !reflect.DeepEqual(req.Features, project.Features) {
		return BadRequest(fmt.Errorf("Features can only be changed on empty projects"))
	}

	// Update the database entry
	err = d.cluster.ProjectUpdate(name, req)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func apiProjectPatch(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	// Get the current data
	_, project, err := d.cluster.ProjectGet(name)
	if err != nil {
		return SmartError(err)
	}

	// Validate ETag
	etag := []interface{}{project.Description, *project.Features}
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

	features, err := reqRaw.GetMap("features")
	if err == nil {
		_, err = features.GetBool("images")
		if err != nil {
			req.Features.Images = project.Features.Images
		}

		_, err = features.GetBool("profiles")
		if err != nil {
			req.Features.Profiles = project.Features.Profiles
		}
	} else {
		req.Features = project.Features
	}

	// Sanity checks
	if project.Name == "default" && !reflect.DeepEqual(req.Features, project.Features) {
		return BadRequest(fmt.Errorf("You can't change the features of the default project"))
	}

	if len(project.UsedBy) != 0 && !reflect.DeepEqual(req.Features, project.Features) {
		return BadRequest(fmt.Errorf("Features can only be changed on empty projects"))
	}

	// Update the database entry
	err = d.cluster.ProjectUpdate(name, req)
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

	_, newProject, err := d.cluster.ProjectGet(req.Name)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return InternalError(err)
		}

		if newProject != nil {
			return BadRequest(fmt.Errorf("A project named '%s' already exists", req.Name))
		}
	}

	// FIXME: Allow renaming non-empty projects
	_, project, err := d.cluster.ProjectGet(name)
	if err != nil {
		return SmartError(err)
	}

	if len(project.UsedBy) != 0 {
		return BadRequest(fmt.Errorf("Only empty projects can be renamed at this time"))
	}

	// Perform the rename
	run := func(op *operation) error {
		return d.cluster.ProjectRename(name, req)
	}

	op, err := operationCreate(d.cluster, operationClassTask, db.OperationProjectRename, nil, nil, run, nil, nil)
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

	_, project, err := d.cluster.ProjectGet(name)
	if err != nil {
		return SmartError(err)
	}

	if len(project.UsedBy) != 0 {
		return BadRequest(fmt.Errorf("Only empty projects can be removed"))
	}

	// Perform the removal
	err = d.cluster.ProjectDelete(name)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}
