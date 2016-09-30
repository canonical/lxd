package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"

	"github.com/dustinkirkland/golang-petname"
	"github.com/gorilla/websocket"
	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

type containerImageSource struct {
	Type        string `json:"type"`
	Certificate string `json:"certificate"`

	/* for "image" type */
	Alias       string            `json:"alias"`
	Fingerprint string            `json:"fingerprint"`
	Properties  map[string]string `json:"properties"`
	Server      string            `json:"server"`
	Secret      string            `json:"secret"`
	Protocol    string            `json:"protocol"`

	/*
	 * for "migration" and "copy" types, as an optimization users can
	 * provide an image hash to extract before the filesystem is rsync'd,
	 * potentially cutting down filesystem transfer time. LXD will not go
	 * and fetch this image, it will simply use it if it exists in the
	 * image store.
	 */
	BaseImage string `json:"base-image"`

	/* for "migration" type */
	Mode       string            `json:"mode"`
	Operation  string            `json:"operation"`
	Websockets map[string]string `json:"secrets"`

	/* for "copy" type */
	Source string `json:"source"`
}

type containerPostReq struct {
	Architecture string               `json:"architecture"`
	Config       map[string]string    `json:"config"`
	Devices      shared.Devices       `json:"devices"`
	Ephemeral    bool                 `json:"ephemeral"`
	Name         string               `json:"name"`
	Profiles     []string             `json:"profiles"`
	Source       containerImageSource `json:"source"`
}

func createFromImage(d *Daemon, req *containerPostReq) Response {
	var hash string
	var err error

	if req.Source.Fingerprint != "" {
		hash = req.Source.Fingerprint
	} else if req.Source.Alias != "" {
		if req.Source.Server != "" {
			hash = req.Source.Alias
		} else {
			_, alias, err := dbImageAliasGet(d.db, req.Source.Alias, true)
			if err != nil {
				return InternalError(err)
			}

			hash = alias.Target
		}
	} else if req.Source.Fingerprint != "" {
		hash = req.Source.Fingerprint
	} else if req.Source.Properties != nil {
		if req.Source.Server != "" {
			return BadRequest(fmt.Errorf("Property match is only supported for local images"))
		}

		hashes, err := dbImagesGet(d.db, false)
		if err != nil {
			return InternalError(err)
		}

		var image *shared.ImageInfo

		for _, hash := range hashes {
			_, img, err := dbImageGet(d.db, hash, false, true)
			if err != nil {
				continue
			}

			if image != nil && img.CreationDate.Before(image.CreationDate) {
				continue
			}

			match := true
			for key, value := range req.Source.Properties {
				if img.Properties[key] != value {
					match = false
					break
				}
			}

			if !match {
				continue
			}

			image = img
		}

		if image == nil {
			return BadRequest(fmt.Errorf("No matching image could be found"))
		}

		hash = image.Fingerprint
	} else {
		return BadRequest(fmt.Errorf("Must specify one of alias, fingerprint or properties for init from image"))
	}

	run := func(op *operation) error {
		if req.Source.Server != "" {
			hash, err = d.ImageDownload(
				op, req.Source.Server, req.Source.Protocol, req.Source.Certificate, req.Source.Secret,
				hash, true, daemonConfig["images.auto_update_cached"].GetBool())
			if err != nil {
				return err
			}
		}

		_, imgInfo, err := dbImageGet(d.db, hash, false, false)
		if err != nil {
			return err
		}

		hash = imgInfo.Fingerprint

		architecture, err := shared.ArchitectureId(imgInfo.Architecture)
		if err != nil {
			architecture = 0
		}

		args := containerArgs{
			Architecture: architecture,
			BaseImage:    hash,
			Config:       req.Config,
			Ctype:        cTypeRegular,
			Devices:      req.Devices,
			Ephemeral:    req.Ephemeral,
			Name:         req.Name,
			Profiles:     req.Profiles,
		}

		_, err = containerCreateFromImage(d, args, hash)
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
	architecture, err := shared.ArchitectureId(req.Architecture)
	if err != nil {
		architecture = 0
	}

	args := containerArgs{
		Architecture: architecture,
		Config:       req.Config,
		Ctype:        cTypeRegular,
		Devices:      req.Devices,
		Ephemeral:    req.Ephemeral,
		Name:         req.Name,
		Profiles:     req.Profiles,
	}

	run := func(op *operation) error {
		_, err := containerCreateAsEmpty(d, args)
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
	if req.Source.Mode != "pull" && req.Source.Mode != "push" {
		return NotImplemented
	}

	architecture, err := shared.ArchitectureId(req.Architecture)
	if err != nil {
		architecture = 0
	}

	args := containerArgs{
		Architecture: architecture,
		BaseImage:    req.Source.BaseImage,
		Config:       req.Config,
		Ctype:        cTypeRegular,
		Devices:      req.Devices,
		Ephemeral:    req.Ephemeral,
		Name:         req.Name,
		Profiles:     req.Profiles,
	}

	var c container
	_, _, err = dbImageGet(d.db, req.Source.BaseImage, false, true)

	/* Only create a container from an image if we're going to
	 * rsync over the top of it. In the case of a better file
	 * transfer mechanism, let's just use that.
	 *
	 * TODO: we could invent some negotiation here, where if the
	 * source and sink both have the same image, we can clone from
	 * it, but we have to know before sending the snapshot that
	 * we're sending the whole thing or just a delta from the
	 * image, so one extra negotiation round trip is needed. An
	 * alternative is to move actual container object to a later
	 * point and just negotiate it over the migration control
	 * socket. Anyway, it'll happen later :)
	 */
	if err == nil && d.Storage.MigrationType() == MigrationFSType_RSYNC {
		c, err = containerCreateFromImage(d, args, req.Source.BaseImage)
		if err != nil {
			return InternalError(err)
		}
	} else {
		c, err = containerCreateAsEmpty(d, args)
		if err != nil {
			return InternalError(err)
		}
	}

	var cert *x509.Certificate
	if req.Source.Certificate != "" {
		certBlock, _ := pem.Decode([]byte(req.Source.Certificate))
		if certBlock == nil {
			return InternalError(fmt.Errorf("Invalid certificate"))
		}

		cert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return InternalError(err)
		}
	}

	config, err := shared.GetTLSConfig("", "", "", cert)
	if err != nil {
		c.Delete()
		return InternalError(err)
	}

	push := false
	if req.Source.Mode == "push" {
		push = true
	}

	migrationArgs := MigrationSinkArgs{
		Url: req.Source.Operation,
		Dialer: websocket.Dialer{
			TLSClientConfig: config,
			NetDial:         shared.RFC3493Dialer},
		Container: c,
		Secrets:   req.Source.Websockets,
		Push:      push,
	}

	sink, err := NewMigrationSink(&migrationArgs)
	if err != nil {
		c.Delete()
		return InternalError(err)
	}

	run := func(op *operation) error {
		// Start the storage for this container (LVM mount/umount)
		c.StorageStart()

		// And finaly run the migration.
		err = sink.Do(op)
		if err != nil {
			c.StorageStop()
			shared.LogError("Error during migration sink", log.Ctx{"err": err})
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

	var op *operation
	if push {
		op, err = operationCreate(operationClassWebsocket, resources, sink.Metadata(), run, nil, sink.Connect)
		if err != nil {
			return InternalError(err)
		}
	} else {
		op, err = operationCreate(operationClassTask, resources, nil, run, nil, nil)
		if err != nil {
			return InternalError(err)
		}
	}

	return OperationResponse(op)
}

func createFromCopy(d *Daemon, req *containerPostReq) Response {
	if req.Source.Source == "" {
		return BadRequest(fmt.Errorf("must specify a source container"))
	}

	source, err := containerLoadByName(d, req.Source.Source)
	if err != nil {
		return SmartError(err)
	}

	// Config override
	sourceConfig := source.LocalConfig()

	if req.Config == nil {
		req.Config = make(map[string]string)
	}

	for key, value := range sourceConfig {
		if len(key) > 8 && key[0:8] == "volatile" && key[9:] != "base_image" {
			shared.LogDebug("Skipping volatile key from copy source",
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

	args := containerArgs{
		Architecture: source.Architecture(),
		BaseImage:    req.Source.BaseImage,
		Config:       req.Config,
		Ctype:        cTypeRegular,
		Devices:      source.LocalDevices(),
		Ephemeral:    req.Ephemeral,
		Name:         req.Name,
		Profiles:     req.Profiles,
	}

	run := func(op *operation) error {
		_, err := containerCreateAsCopy(d, args, source)
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
	shared.LogDebugf("Responding to container create")

	req := containerPostReq{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return BadRequest(err)
	}

	if req.Name == "" {
		cs, err := dbContainersList(d.db, cTypeRegular)
		if err != nil {
			return InternalError(err)
		}

		i := 0
		for {
			i++
			req.Name = strings.ToLower(petname.Generate(2, "-"))
			if !shared.StringInSlice(req.Name, cs) {
				break
			}

			if i > 100 {
				return InternalError(fmt.Errorf("couldn't generate a new unique name after 100 tries"))
			}
		}
		shared.LogDebugf("No name provided, creating %s", req.Name)
	}

	if req.Devices == nil {
		req.Devices = shared.Devices{}
	}

	if req.Config == nil {
		req.Config = map[string]string{}
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
