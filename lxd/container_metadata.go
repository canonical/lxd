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

	"gopkg.in/yaml.v2"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func containerMetadataGet(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	// Load the container
	c, err := containerLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return SmartError(err)
	}
	metadataPath := filepath.Join(c.Path(), "metadata.yaml")

	// Start the storage if needed
	ourStart, err := c.StorageStart()
	if err != nil {
		return SmartError(err)
	}
	if ourStart {
		defer c.StorageStop()
	}

	// Read the metadata
	metadataFile, err := os.Open(metadataPath)
	if err != nil {
		return InternalError(err)
	}
	defer metadataFile.Close()

	data, err := ioutil.ReadAll(metadataFile)
	if err != nil {
		return InternalError(err)
	}

	// Parse into the API struct
	metadata := api.ImageMetadata{}
	err = yaml.Unmarshal(data, &metadata)
	if err != nil {
		return SmartError(err)
	}

	return SyncResponse(true, metadata)
}

func containerMetadataPut(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	// Load the container
	c, err := containerLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return SmartError(err)
	}
	metadataPath := filepath.Join(c.Path(), "metadata.yaml")

	// Start the storage if needed
	ourStart, err := c.StorageStart()
	if err != nil {
		return SmartError(err)
	}
	if ourStart {
		defer c.StorageStop()
	}

	// Read the new metadata
	metadata := api.ImageMetadata{}
	if err := json.NewDecoder(r.Body).Decode(&metadata); err != nil {
		return BadRequest(err)
	}

	// Write as YAML
	data, err := yaml.Marshal(metadata)
	if err != nil {
		return BadRequest(err)
	}

	if err := ioutil.WriteFile(metadataPath, data, 0644); err != nil {
		InternalError(err)
	}

	return EmptySyncResponse
}

// Return a list of templates used in a container or the content of a template
func containerMetadataTemplatesGet(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	// Load the container
	c, err := containerLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return SmartError(err)
	}

	// Start the storage if needed
	ourStart, err := c.StorageStart()
	if err != nil {
		return SmartError(err)
	}
	if ourStart {
		defer c.StorageStop()
	}

	// Look at the request
	templateName := r.FormValue("path")
	if templateName == "" {
		// List templates
		templatesPath := filepath.Join(c.Path(), "templates")
		filesInfo, err := ioutil.ReadDir(templatesPath)
		if err != nil {
			return InternalError(err)
		}

		templates := []string{}
		for _, info := range filesInfo {
			if !info.IsDir() {
				templates = append(templates, info.Name())
			}
		}

		return SyncResponse(true, templates)
	}

	// Check if the template exists
	templatePath, err := getContainerTemplatePath(c, templateName)
	if err != nil {
		return SmartError(err)
	}

	if !shared.PathExists(templatePath) {
		return NotFound(fmt.Errorf("Path '%s' not found", templatePath))
	}

	// Create a temporary file with the template content (since the container
	// storage might not be available when the file is read from FileResponse)
	template, err := os.Open(templatePath)
	if err != nil {
		return SmartError(err)
	}
	defer template.Close()

	tempfile, err := ioutil.TempFile("", "lxd_template")
	if err != nil {
		return SmartError(err)
	}
	defer tempfile.Close()

	_, err = io.Copy(tempfile, template)
	if err != nil {
		return InternalError(err)
	}

	files := make([]fileResponseEntry, 1)
	files[0].identifier = templateName
	files[0].path = tempfile.Name()
	files[0].filename = templateName
	return FileResponse(r, files, nil, true)
}

// Add a container template file
func containerMetadataTemplatesPostPut(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	// Load the container
	c, err := containerLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return SmartError(err)
	}

	// Start the storage if needed
	ourStart, err := c.StorageStart()
	if err != nil {
		return SmartError(err)
	}
	if ourStart {
		defer c.StorageStop()
	}

	// Look at the request
	templateName := r.FormValue("path")
	if templateName == "" {
		return BadRequest(fmt.Errorf("missing path argument"))
	}

	// Check if the template already exists
	templatePath, err := getContainerTemplatePath(c, templateName)
	if err != nil {
		return SmartError(err)
	}

	if r.Method == "POST" && shared.PathExists(templatePath) {
		return BadRequest(fmt.Errorf("Template already exists"))
	}

	// Write the new template
	template, err := os.OpenFile(templatePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return SmartError(err)
	}
	defer template.Close()

	_, err = io.Copy(template, r.Body)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

// Delete a container template
func containerMetadataTemplatesDelete(d *Daemon, r *http.Request) Response {
	project := projectParam(r)

	name := mux.Vars(r)["name"]

	// Handle requests targeted to a container on a different node
	response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name)
	if err != nil {
		return SmartError(err)
	}
	if response != nil {
		return response
	}

	// Load the container
	c, err := containerLoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return SmartError(err)
	}

	// Start the storage if needed
	ourStart, err := c.StorageStart()
	if err != nil {
		return SmartError(err)
	}
	if ourStart {
		defer c.StorageStop()
	}

	// Look at the request
	templateName := r.FormValue("path")
	if templateName == "" {
		return BadRequest(fmt.Errorf("missing path argument"))
	}

	templatePath, err := getContainerTemplatePath(c, templateName)
	if err != nil {
		return SmartError(err)
	}

	if !shared.PathExists(templatePath) {
		return NotFound(fmt.Errorf("Path '%s' not found", templatePath))
	}

	// Delete the template
	err = os.Remove(templatePath)
	if err != nil {
		return InternalError(err)
	}

	return EmptySyncResponse
}

// Return the full path of a container template.
func getContainerTemplatePath(c container, filename string) (string, error) {
	if strings.Contains(filename, "/") {
		return "", fmt.Errorf("Invalid template filename")
	}

	return filepath.Join(c.Path(), "templates", filename), nil
}
