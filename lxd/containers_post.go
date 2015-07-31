package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/dustinkirkland/golang-petname"
	"github.com/gorilla/websocket"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/shared"
)

func createFromImage(d *Daemon, req *containerPostReq) Response {
	var hash string
	var err error
	var run func() shared.OperationResult

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

	if req.Source.Server != "" {
		err := ensureLocalImage(d, req.Source.Server, hash, req.Source.Secret)
		if err != nil {
			return InternalError(err)
		}
	}

	imgInfo, err := dbImageGet(d.db, hash, false)
	if err != nil {
		return SmartError(err)
	}
	hash = imgInfo.Fingerprint

	args := containerLXDArgs{
		Ctype:        cTypeRegular,
		Config:       req.Config,
		Profiles:     req.Profiles,
		Ephemeral:    req.Ephemeral,
		BaseImage:    hash,
		Architecture: imgInfo.Architecture,
	}

	c, err := containerLXDCreate(d, req.Name, args)
	if err != nil {
		c.Delete()
		return SmartError(err)
	}

	run = shared.OperationWrap(func() error {
		err := c.StorageFromImage(hash)
		if err != nil {
			c.Delete()
		}
		return err
	})

	resources := make(map[string][]string)
	resources["containers"] = []string{c.NameGet()}

	return &asyncResponse{run: run, resources: resources}
}

func createFromNone(d *Daemon, req *containerPostReq) Response {
	args := containerLXDArgs{
		Ctype:     cTypeRegular,
		Config:    req.Config,
		Profiles:  req.Profiles,
		Ephemeral: req.Ephemeral,
	}

	run := shared.OperationWrap(func() error {
		c, err := containerLXDCreate(d, req.Name, args)
		if err != nil {
			return err
		}

		if err := c.StorageFromNone(); err != nil {
			c.Delete()
			return err
		}

		return nil
	})

	resources := make(map[string][]string)
	resources["containers"] = []string{req.Name}

	return &asyncResponse{run: run, resources: resources}
}

func createFromMigration(d *Daemon, req *containerPostReq) Response {
	if req.Source.Mode != "pull" {
		return NotImplemented
	}

	createArgs := containerLXDArgs{
		Ctype:     cTypeRegular,
		Config:    req.Config,
		Profiles:  req.Profiles,
		Ephemeral: req.Ephemeral,
		BaseImage: req.Source.BaseImage,
	}

	c, err := containerLXDCreate(d, req.Name, createArgs)
	if err != nil {
		c.Delete()
		return SmartError(err)
	}

	_, err = dbImageGet(d.db, req.Source.BaseImage, false)
	if err == nil {
		if err := c.StorageFromImage(req.Source.BaseImage); err != nil {
			c.Delete()
			return InternalError(err)
		}
	} else {
		if err := c.StorageFromNone(); err != nil {
			c.Delete()
			return InternalError(err)
		}
	}

	config, err := shared.GetTLSConfig(d.certf, d.keyf)
	if err != nil {
		c.Delete()
		return InternalError(err)
	}

	lxContainer, err := c.LXContainerGet()
	if err != nil {
		c.Delete()
		return InternalError(err)
	}
	idmapset, err := c.IdmapSetGet()
	if err != nil {
		c.Delete()
		return InternalError(err)
	}
	args := migration.MigrationSinkArgs{
		Url: req.Source.Operation,
		Dialer: websocket.Dialer{
			TLSClientConfig: config,
			NetDial:         shared.RFC3493Dialer},
		Container: lxContainer,
		Secrets:   req.Source.Websockets,
		IdMapSet:  idmapset,
	}

	sink, err := migration.NewMigrationSink(&args)
	if err != nil {
		c.Delete()
		return BadRequest(err)
	}

	run := func() shared.OperationResult {
		// Start the storage for this container (LVM mount/umount)
		c.StorageStart()
		defer c.StorageStop()

		err := sink()
		if err != nil {
			c.Delete()
			return shared.OperationError(err)
		}

		c, err := containerLXDLoad(d, req.Name)
		if err != nil {
			return shared.OperationError(err)
		}

		err = c.TemplateApply("copy")
		if err != nil {
			return shared.OperationError(err)
		}

		return shared.OperationError(nil)
	}

	resources := make(map[string][]string)
	resources["containers"] = []string{req.Name}

	return &asyncResponse{run: run, resources: resources}
}

func createFromCopy(d *Daemon, req *containerPostReq) Response {
	if req.Source.Source == "" {
		return BadRequest(fmt.Errorf("must specify a source container"))
	}

	// Make sure the source exists.
	source, err := containerLXDLoad(d, req.Source.Source)
	if err != nil {
		return SmartError(err)
	}

	sourceConfig := source.ConfigGet()

	if req.Config == nil {
		config := make(map[string]string)
		for key, value := range sourceConfig.Config {
			if key[0:8] == "volatile" {
				shared.Debugf("skipping: %s\n", key)
				continue
			}
			req.Config[key] = value
		}
		req.Config = config
	}

	if req.Profiles == nil {
		req.Profiles = sourceConfig.Profiles
	}

	args := containerLXDArgs{
		Ctype:     cTypeRegular,
		Config:    req.Config,
		Profiles:  req.Profiles,
		Ephemeral: req.Ephemeral,
		BaseImage: req.Source.BaseImage,
	}

	run := func() shared.OperationResult {
		c, err := containerLXDCreate(d, req.Name, args)
		if err != nil {
			return shared.OperationError(err)
		}

		if err := c.Copy(source); err != nil {
			c.Delete()
			return shared.OperationError(err)
		}

		return shared.OperationSuccess
	}

	resources := make(map[string][]string)
	resources["containers"] = []string{req.Name, req.Source.Source}

	return &asyncResponse{run: run, resources: resources}
}

func containersPost(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to create")

	if d.IdmapSet == nil {
		return BadRequest(fmt.Errorf("shared's user has no subuids"))
	}

	req := containerPostReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	if req.Name == "" {
		req.Name = strings.ToLower(petname.Generate(2, "-"))
		shared.Debugf("no name provided, creating %s", req.Name)
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
