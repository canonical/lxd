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

	log "gopkg.in/inconshreveable/log15.v2"
)

func shiftRootfs(c container, d *Daemon) error {
	dpath := c.PathGet("")
	rpath := c.RootfsPathGet()

	shared.Log.Debug("shiftRootfs",
		log.Ctx{"container": c.NameGet(), "rootfs": rpath})

	idmapset, err := c.IdmapSetGet()
	if err != nil {
		return err
	}

	if idmapset == nil {
		return fmt.Errorf("IdmapSet of container '%s' is nil", c.NameGet())
	}

	err = idmapset.ShiftRootfs(rpath)
	if err != nil {
		shared.Debugf("Shift of rootfs %s failed: %s\n", rpath, err)
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

func setUnprivUserAcl(c container, dpath string) error {
	idmapset, err := c.IdmapSetGet()
	if err != nil {
		return err
	}

	if idmapset == nil {
		return nil
	}
	uid, _ := idmapset.ShiftIntoNs(0, 0)
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

func extractShiftIfExists(d *Daemon, c container, hash string, name string) error {
	if hash == "" {
		return nil
	}

	_, err := dbImageGet(d.db, hash, false)
	if err == nil {
		imagefile := shared.VarPath("images", hash)
		dpath := c.PathGet("")
		if err := untarImage(imagefile, dpath); err != nil {
			return err
		}

		if !c.IsPrivileged() {
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

	dpath := shared.VarPath("containers", req.Name)
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
		c.Delete()
		return SmartError(err)
	}

	run = shared.OperationWrap(func() error {
		err := c.CreateFromImage(hash)
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

	_, err := dbContainerCreate(d.db, req.Name, args)
	if err != nil {
		return SmartError(err)
	}

	run := shared.OperationWrap(func() error {
		c, err := newLxdContainer(req.Name, d)
		if err != nil {
			return err
		}

		err = c.TemplateApply("create")
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
		c.Delete()
		return SmartError(err)
	}

	// rsync complaisn if the parent directory for the rootfs sync doesn't
	// exist
	dpath := shared.VarPath("containers", req.Name)
	if err := os.MkdirAll(dpath, 0700); err != nil {
		c.Delete()
		return InternalError(err)
	}

	if err := extractShiftIfExists(d, c, req.Source.BaseImage, req.Name); err != nil {
		c.Delete()
		return InternalError(err)
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
		err := sink()
		if err != nil {
			c.Delete()
			return shared.OperationError(err)
		}

		c, err := newLxdContainer(req.Name, d)
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
	source, err := newLxdContainer(req.Source.Source, d)
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
		c, err := createcontainerLXD(d, req.Name, args)
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
