package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/dustinkirkland/golang-petname"
	"github.com/gorilla/websocket"
	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

func createFromImage(d *Daemon, req *containerPostReq) Response {
	var hash string
	var err error

	if req.Source.Alias != "" {
		if req.Source.Mode == "pull" && req.Source.Server != "" {
			hash, err = remoteGetImageFingerprint(d, req.Source.Server, req.Source.Alias)
			if err != nil {
				return InternalError(err)
			}
		} else {

			hash, err = dbImageAliasGet(d.db, req.Source.Alias)
			if err != nil {
				return InternalError(err)
			}
		}
	} else if req.Source.Fingerprint != "" {
		hash = req.Source.Fingerprint
	} else {
		return BadRequest(fmt.Errorf("must specify one of alias or fingerprint for init from image"))
	}

	run := func(op *operation) error {
		if req.Source.Server != "" {
			err := d.ImageDownload(op, req.Source.Server, hash, req.Source.Secret, true, false)
			if err != nil {
				return err
			}
		}

		imgInfo, err := dbImageGet(d.db, hash, false, false)
		if err != nil {
			return err
		}

		hash = imgInfo.Fingerprint

		args := containerLXDArgs{
			Architecture: imgInfo.Architecture,
			BaseImage:    hash,
			Config:       req.Config,
			Ctype:        cTypeRegular,
			Ephemeral:    req.Ephemeral,
			Profiles:     req.Profiles,
		}

		_, err = containerLXDCreateFromImage(d, req.Name, args, hash)
		return err
	}

	resources := map[string][]string{}
	resources["containers"] = []string{req.Name}

	op, err := operationCreate(operationClassTask, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func createFromNone(d *Daemon, req *containerPostReq) Response {
	args := containerLXDArgs{
		Architecture: req.Architecture,
		Config:       req.Config,
		Ctype:        cTypeRegular,
		Ephemeral:    req.Ephemeral,
		Profiles:     req.Profiles,
	}

	run := func(op *operation) error {
		_, err := containerLXDCreateAsEmpty(d, req.Name, args)
		return err
	}

	resources := map[string][]string{}
	resources["containers"] = []string{req.Name}

	op, err := operationCreate(operationClassTask, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func createFromMigration(d *Daemon, req *containerPostReq) Response {
	if req.Source.Mode != "pull" {
		return NotImplemented
	}

	run := func(op *operation) error {
		createArgs := containerLXDArgs{
			Architecture: req.Architecture,
			BaseImage:    req.Source.BaseImage,
			Config:       req.Config,
			Ctype:        cTypeRegular,
			Devices:      req.Devices,
			Ephemeral:    req.Ephemeral,
			Profiles:     req.Profiles,
		}

		var c container
		if _, err := dbImageGet(d.db, req.Source.BaseImage, false, true); err == nil {
			c, err = containerLXDCreateFromImage(
				d, req.Name, createArgs, req.Source.BaseImage)

			if err != nil {
				return err
			}
		} else {
			c, err = containerLXDCreateAsEmpty(d, req.Name, createArgs)
			if err != nil {
				return err
			}
		}

		config, err := shared.GetTLSConfig(d.certf, d.keyf)
		if err != nil {
			c.Delete()
			return err
		}

		args := MigrationSinkArgs{
			Url: req.Source.Operation,
			Dialer: websocket.Dialer{
				TLSClientConfig: config,
				NetDial:         shared.RFC3493Dialer},
			Container: c,
			Secrets:   req.Source.Websockets,
		}

		sink, err := NewMigrationSink(&args)
		if err != nil {
			c.Delete()
			return err
		}

		// Start the storage for this container (LVM mount/umount)
		c.StorageStart()

		// And finaly run the migration.
		err = sink()
		if err != nil {
			c.StorageStop()
			c.Delete()
			return fmt.Errorf("Error transferring container data: %s", err)
		}

		defer c.StorageStop()

		err = c.TemplateApply("copy")
		if err != nil {
			return err
		}

		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{req.Name}

	op, err := operationCreate(operationClassTask, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func createFromCopy(d *Daemon, req *containerPostReq) Response {
	if req.Source.Source == "" {
		return BadRequest(fmt.Errorf("must specify a source container"))
	}

	source, err := containerLXDLoad(d, req.Source.Source)
	if err != nil {
		return SmartError(err)
	}

	// Config override
	sourceConfig := source.BaseConfig()

	if req.Config == nil {
		req.Config = make(map[string]string)
	}

	for key, value := range sourceConfig {
		if len(key) > 8 && key[0:8] == "volatile" && key[9:] != "base_image" {
			shared.Log.Debug("Skipping volatile key from copy source",
				log.Ctx{"key": key})
			continue
		}

		_, exists := req.Config[key]
		if exists {
			continue
		}

		req.Config[key] = value
	}

	// Profiles override
	if req.Profiles == nil {
		req.Profiles = source.Profiles()
	}

	args := containerLXDArgs{
		Architecture: source.Architecture(),
		BaseImage:    req.Source.BaseImage,
		Config:       req.Config,
		Ctype:        cTypeRegular,
		Devices:      source.BaseDevices(),
		Ephemeral:    req.Ephemeral,
		Profiles:     req.Profiles,
	}

	run := func(op *operation) error {
		_, err := containerLXDCreateAsCopy(d, req.Name, args, source)
		if err != nil {
			return err
		}

		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{req.Name, req.Source.Source}

	op, err := operationCreate(operationClassTask, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

func containersPost(d *Daemon, r *http.Request) Response {
	shared.Debugf("Responding to container create")

	req := containerPostReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	if req.Name == "" {
		req.Name = strings.ToLower(petname.Generate(2, "-"))
		shared.Debugf("No name provided, creating %s", req.Name)
	}

	if strings.Contains(req.Name, shared.SnapshotDelimiter) {
		return BadRequest(fmt.Errorf("Invalid container name: '%s' is reserved for snapshots", shared.SnapshotDelimiter))
	}

	switch req.Source.Type {
	case "image":
		return createFromImage(d, &req)
	case "none":
		return createFromNone(d, &req)
	case "migration":
		return createFromMigration(d, &req)
	case "copy":
		return createFromCopy(d, &req)
	default:
		return BadRequest(fmt.Errorf("unknown source type %s", req.Source.Type))
	}

}
