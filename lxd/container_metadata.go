package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func containerMetadataGet(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	resp, err := ForwardedResponseIfContainerIsRemote(d, r, project, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}
	if resp != nil {
		return resp
	}

	// Load the container
	c, err := instanceLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}
	metadataPath := filepath.Join(c.Path(), "metadata.yaml")

	// Start the storage if needed
	ourStart, err := c.StorageStart()
	if err != nil {
		return response.SmartError(err)
	}
	if ourStart {
		defer c.StorageStop()
	}

	// If missing, just return empty result
	if !shared.PathExists(metadataPath) {
		return response.SyncResponse(true, api.ImageMetadata{})
	}

	// Read the metadata
	metadataFile, err := os.Open(metadataPath)
	if err != nil {
		return response.InternalError(err)
	}
	defer metadataFile.Close()

	data, err := ioutil.ReadAll(metadataFile)
	if err != nil {
		return response.InternalError(err)
	}

	// Parse into the API struct
	metadata := api.ImageMetadata{}
	err = yaml.Unmarshal(data, &metadata)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, metadata)
}

func containerMetadataPut(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	resp, err := ForwardedResponseIfContainerIsRemote(d, r, project, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}
	if resp != nil {
		return resp
	}

	// Load the container
	c, err := instanceLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}
	metadataPath := filepath.Join(c.Path(), "metadata.yaml")

	// Start the storage if needed
	ourStart, err := c.StorageStart()
	if err != nil {
		return response.SmartError(err)
	}
	if ourStart {
		defer c.StorageStop()
	}

	// Read the new metadata
	metadata := api.ImageMetadata{}
	if err := json.NewDecoder(r.Body).Decode(&metadata); err != nil {
		return response.BadRequest(err)
	}

	// Write as YAML
	data, err := yaml.Marshal(metadata)
	if err != nil {
		return response.BadRequest(err)
	}

	if err := ioutil.WriteFile(metadataPath, data, 0644); err != nil {
		return response.InternalError(err)
	}

	return response.EmptySyncResponse
}

// Return a list of templates used in a container or the content of a template
func containerMetadataTemplatesGet(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	resp, err := ForwardedResponseIfContainerIsRemote(d, r, project, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}
	if resp != nil {
		return resp
	}

	// Load the container
	c, err := instanceLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Start the storage if needed
	ourStart, err := c.StorageStart()
	if err != nil {
		return response.SmartError(err)
	}
	if ourStart {
		defer c.StorageStop()
	}

	// Look at the request
	templateName := r.FormValue("path")
	if templateName == "" {
		templates := []string{}
		if !shared.PathExists(filepath.Join(c.Path(), "templates")) {
			return response.SyncResponse(true, templates)
		}

		// List templates
		templatesPath := filepath.Join(c.Path(), "templates")
		filesInfo, err := ioutil.ReadDir(templatesPath)
		if err != nil {
			return response.InternalError(err)
		}

		for _, info := range filesInfo {
			if !info.IsDir() {
				templates = append(templates, info.Name())
			}
		}

		return response.SyncResponse(true, templates)
	}

	// Check if the template exists
	templatePath, err := getContainerTemplatePath(c, templateName)
	if err != nil {
		return response.SmartError(err)
	}

	if !shared.PathExists(templatePath) {
		return response.NotFound(fmt.Errorf("Template '%s' not found", templateName))
	}

	// Create a temporary file with the template content (since the container
	// storage might not be available when the file is read from FileResponse)
	template, err := os.Open(templatePath)
	if err != nil {
		return response.SmartError(err)
	}
	defer template.Close()

	tempfile, err := ioutil.TempFile("", "lxd_template")
	if err != nil {
		return response.SmartError(err)
	}
	defer tempfile.Close()

	_, err = io.Copy(tempfile, template)
	if err != nil {
		return response.InternalError(err)
	}

	files := make([]response.FileResponseEntry, 1)
	files[0].Identifier = templateName
	files[0].Path = tempfile.Name()
	files[0].Filename = templateName
	return response.FileResponse(r, files, nil, true)
}

// Add a container template file
func containerMetadataTemplatesPostPut(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	resp, err := ForwardedResponseIfContainerIsRemote(d, r, project, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}
	if resp != nil {
		return resp
	}

	// Load the container
	c, err := instanceLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Start the storage if needed
	ourStart, err := c.StorageStart()
	if err != nil {
		return response.SmartError(err)
	}
	if ourStart {
		defer c.StorageStop()
	}

	// Look at the request
	templateName := r.FormValue("path")
	if templateName == "" {
		return response.BadRequest(fmt.Errorf("missing path argument"))
	}

	if !shared.PathExists(filepath.Join(c.Path(), "templates")) {
		err := os.MkdirAll(filepath.Join(c.Path(), "templates"), 0711)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Check if the template already exists
	templatePath, err := getContainerTemplatePath(c, templateName)
	if err != nil {
		return response.SmartError(err)
	}

	if r.Method == "POST" && shared.PathExists(templatePath) {
		return response.BadRequest(fmt.Errorf("Template already exists"))
	}

	// Write the new template
	template, err := os.OpenFile(templatePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return response.SmartError(err)
	}
	defer template.Close()

	_, err = io.Copy(template, r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	return response.EmptySyncResponse
}

// Delete a container template
func containerMetadataTemplatesDelete(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	project := projectParam(r)

	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	resp, err := ForwardedResponseIfContainerIsRemote(d, r, project, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}
	if resp != nil {
		return resp
	}

	// Load the container
	c, err := instanceLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Start the storage if needed
	ourStart, err := c.StorageStart()
	if err != nil {
		return response.SmartError(err)
	}
	if ourStart {
		defer c.StorageStop()
	}

	// Look at the request
	templateName := r.FormValue("path")
	if templateName == "" {
		return response.BadRequest(fmt.Errorf("missing path argument"))
	}

	templatePath, err := getContainerTemplatePath(c, templateName)
	if err != nil {
		return response.SmartError(err)
	}

	if !shared.PathExists(templatePath) {
		return response.NotFound(fmt.Errorf("Template '%s' not found", templateName))
	}

	// Delete the template
	err = os.Remove(templatePath)
	if err != nil {
		return response.InternalError(err)
	}

	return response.EmptySyncResponse
}

// Return the full path of a container template.
func getContainerTemplatePath(c Instance, filename string) (string, error) {
	if strings.Contains(filename, "/") {
		return "", fmt.Errorf("Invalid template filename")
	}

	return filepath.Join(c.Path(), "templates", filename), nil
}
