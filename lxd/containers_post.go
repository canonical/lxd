package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/dustinkirkland/golang-petname"
	"github.com/gorilla/websocket"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/shared"
)

func extractImage(hash string, name string, d *Daemon) error {
	/*
	 * We want to use archive/tar for this, but that doesn't appear
	 * to be working for us (see lxd/images.go)
	 */
	dpath := shared.VarPath("lxc", name)
	imagefile := shared.VarPath("images", hash)

	err := untar(imagefile, dpath)
	if err != nil {
		return err
	}

	if shared.PathExists(imagefile + ".rootfs") {
		err := untar(imagefile+".rootfs", dpath+"/rootfs/")
		if err != nil {
			return err
		}
	}

	return nil
}

func shiftRootfs(c *lxdContainer, d *Daemon) error {
	dpath := shared.VarPath("lxc", c.name)
	rpath := shared.VarPath("lxc", c.name, "rootfs")
	err := c.idmapset.ShiftRootfs(rpath)
	if err != nil {
		shared.Debugf("Shift of rootfs %s failed: %s\n", rpath, err)
		removeContainer(d, c)
		return err
	}

	/* Set an acl so the container root can descend the container dir */
	err = setUnprivUserAcl(c, dpath)
	if err != nil {
		shared.Debugf("Error adding acl for container root: falling back to chmod\n")
		output, err := exec.Command("chmod", "+x", dpath).CombinedOutput()
		if err != nil {
			shared.Debugf("Error chmoding the container root\n")
			shared.Debugf(string(output))
			return err
		}
	}

	return nil
}

func setUnprivUserAcl(c *lxdContainer, dpath string) error {
	if c.idmapset == nil {
		return nil
	}
	uid, _ := c.idmapset.ShiftIntoNs(0, 0)
	switch uid {
	case -1:
		shared.Debugf("setUnprivUserAcl: no root id mapping")
		return nil
	case 0:
		return nil
	}
	acl := fmt.Sprintf("%d:rx", uid)
	output, err := exec.Command("setfacl", "-m", acl, dpath).CombinedOutput()
	if err != nil {
		shared.Debugf("setfacl failed:\n%s", output)
	}
	return err
}

func extractShiftIfExists(d *Daemon, c *lxdContainer, hash string, name string) error {
	if hash == "" {
		return nil
	}

	_, err := dbImageGet(d.db, hash, false)
	if err == nil {
		if err := extractImage(hash, name, d); err != nil {
			return err
		}

		if !c.isPrivileged() {
			if err := shiftRootfs(c, d); err != nil {
				return err
			}
		}
	}

	return nil
}

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

	dpath := shared.VarPath("lxc", req.Name)
	if shared.PathExists(dpath) {
		return InternalError(fmt.Errorf("Container exists"))
	}

	name := req.Name

	args := containerLXDArgs{
		Ctype:        cTypeRegular,
		Config:       req.Config,
		Profiles:     req.Profiles,
		Ephemeral:    req.Ephemeral,
		BaseImage:    hash,
		Architecture: imgInfo.Architecture,
	}

	_, err = dbContainerCreate(d.db, name, args)
	if err != nil {
		return SmartError(err)
	}

	c, err := newLxdContainer(name, d)
	if err != nil {
		removeContainer(d, c)
		return SmartError(err)
	}

	run = shared.OperationWrap(func() error {
		err := d.Storage.ContainerCreate(c, hash)
		if err != nil {
			removeContainer(d, c)
		}
		return err
	})

	resources := make(map[string][]string)
	resources["containers"] = []string{c.name}

	return &asyncResponse{run: run, resources: resources}
}

func createFromNone(d *Daemon, req *containerPostReq) Response {
	args := containerLXDArgs{
		Ctype:     cTypeRegular,
		Config:    req.Config,
		Profiles:  req.Profiles,
		Ephemeral: req.Ephemeral,
	}

	_, err := dbContainerCreate(d.db, req.Name, args)
	if err != nil {
		return SmartError(err)
	}

	run := shared.OperationWrap(func() error {
		c, err := newLxdContainer(req.Name, d)
		if err != nil {
			return err
		}

		err = templateApply(c, "create")
		if err != nil {
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

	_, err := dbContainerCreate(d.db, req.Name, createArgs)
	if err != nil {
		return SmartError(err)
	}

	c, err := newLxdContainer(req.Name, d)
	if err != nil {
		removeContainer(d, c)
		return SmartError(err)
	}

	// rsync complaisn if the parent directory for the rootfs sync doesn't
	// exist
	dpath := shared.VarPath("lxc", req.Name)
	if err := os.MkdirAll(dpath, 0700); err != nil {
		removeContainer(d, c)
		return InternalError(err)
	}

	if err := extractShiftIfExists(d, c, req.Source.BaseImage, req.Name); err != nil {
		removeContainer(d, c)
		return InternalError(err)
	}

	config, err := shared.GetTLSConfig(d.certf, d.keyf)
	if err != nil {
		removeContainer(d, c)
		return InternalError(err)
	}

	args := migration.MigrationSinkArgs{
		Url: req.Source.Operation,
		Dialer: websocket.Dialer{
			TLSClientConfig: config,
			NetDial:         shared.RFC3493Dialer},
		Container: c.c,
		Secrets:   req.Source.Websockets,
		IdMapSet:  c.idmapset,
	}

	sink, err := migration.NewMigrationSink(&args)
	if err != nil {
		removeContainer(d, c)
		return BadRequest(err)
	}

	run := func() shared.OperationResult {
		err := sink()
		if err != nil {
			removeContainer(d, c)
			return shared.OperationError(err)
		}

		c, err := newLxdContainer(req.Name, d)
		if err != nil {
			return shared.OperationError(err)
		}

		err = templateApply(c, "copy")
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
	source, err := newLxdContainer(req.Source.Source, d)
	if err != nil {
		return SmartError(err)
	}

	if req.Config == nil {
		config := make(map[string]string)
		for key, value := range source.config {
			if key[0:8] == "volatile" {
				shared.Debugf("skipping: %s\n", key)
				continue
			}
			req.Config[key] = value
		}
		req.Config = config
	}

	if req.Profiles == nil {
		req.Profiles = source.profiles
	}

	args := containerLXDArgs{
		Ctype:     cTypeRegular,
		Config:    req.Config,
		Profiles:  req.Profiles,
		Ephemeral: req.Ephemeral,
		BaseImage: req.Source.BaseImage,
	}

	_, err = dbContainerCreate(d.db, req.Name, args)
	if err != nil {
		return SmartError(err)
	}

	run := func() shared.OperationResult {
		c, err := newLxdContainer(req.Name, d)
		if err != nil {
			return shared.OperationError(err)
		}

		s, err := storageForContainer(d, source)
		if err != nil {
			return shared.OperationError(err)
		}
		if err := s.ContainerCopy(c, source); err != nil {
			removeContainer(d, c)
			return shared.OperationError(err)
		}

		return shared.OperationError(nil)
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
