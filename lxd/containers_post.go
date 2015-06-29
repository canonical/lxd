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

func btrfsCopyImage(hash string, name string, d *Daemon) error {
	dpath := shared.VarPath("lxc", name)
	imagefile := shared.VarPath("images", hash)
	subvol := fmt.Sprintf("%s.btrfs", imagefile)

	return exec.Command("btrfs", "subvolume", "snapshot", subvol, dpath).Run()
}

func extractImage(hash string, name string, d *Daemon) error {
	/*
	 * We want to use archive/tar for this, but that doesn't appear
	 * to be working for us (see lxd/images.go)
	 */
	dpath := shared.VarPath("lxc", name)
	imagefile := shared.VarPath("images", hash)

	compression, _, err := detectCompression(imagefile)
	if err != nil {
		shared.Logf("Unkown compression type: %s", err)
		removeContainer(d, name)
		return err
	}

	args := []string{"-C", dpath, "--numeric-owner"}
	switch compression {
	case COMPRESSION_TAR:
		args = append(args, "-xf")
	case COMPRESSION_GZIP:
		args = append(args, "-zxf")
	case COMPRESSION_BZ2:
		args = append(args, "--jxf")
	case COMPRESSION_LZMA:
		args = append(args, "--lzma", "-xf")
	default:
		args = append(args, "-Jxf")
	}
	args = append(args, imagefile)

	output, err := exec.Command("tar", args...).Output()
	if err != nil {
		shared.Debugf("Untar of image: Output %s\nError %s\n", output, err)
		removeContainer(d, name)
		return err
	}

	return nil
}

func shiftRootfs(c *lxdContainer, name string, d *Daemon) error {
	dpath := shared.VarPath("lxc", name)
	rpath := shared.VarPath("lxc", name, "rootfs")
	err := c.idmapset.ShiftRootfs(rpath)
	if err != nil {
		shared.Debugf("Shift of rootfs %s failed: %s\n", rpath, err)
		removeContainer(d, name)
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
			if err := shiftRootfs(c, name, d); err != nil {
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

	backing_fs, err := shared.GetFilesystem(d.lxcpath)
	if err != nil {
		return InternalError(err)
	}

	if req.Source.Alias != "" {
		if req.Source.Mode == "pull" && req.Source.Server != "" {
			hash, err = remoteGetImageFingerprint(d, req.Source.Server, req.Source.Alias)
			if err != nil {
				return InternalError(err)
			}
		} else {

			hash, err = dbAliasGet(d.db, req.Source.Alias)
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

	args := DbCreateContainerArgs{
		d:            d,
		name:         name,
		ctype:        cTypeRegular,
		config:       req.Config,
		profiles:     req.Profiles,
		ephem:        req.Ephemeral,
		baseImage:    hash,
		architecture: imgInfo.Architecture,
	}

	_, err = dbCreateContainer(args)
	if err != nil {
		removeContainerPath(d, name)
		return SmartError(err)
	}

	c, err := newLxdContainer(name, d)
	if err != nil {
		removeContainer(d, name)
		return SmartError(err)
	}

	vgname, vgnameIsSet, err := getServerConfigValue(d, "core.lvm_vg_name")
	if err != nil {
		return InternalError(fmt.Errorf("Error checking server config: %v", err))
	}

	if vgnameIsSet && shared.PathExists(fmt.Sprintf("%s.lv", shared.VarPath("images", hash))) {
		run = shared.OperationWrap(func() error {
			srcLVPath := fmt.Sprintf("/dev/%s/%s", vgname, hash)

			snapshotLVPath, err := shared.LVMCreateSnapshotLV(name, hash, vgname)
			if err != nil {
				return fmt.Errorf("Error creating snapshot of source volume '%s'", srcLVPath)
			}

			destPath := shared.VarPath("lxc", name)
			err = os.MkdirAll(destPath, 0700)
			if err != nil {
				return fmt.Errorf("Error creating container directory: %v", err)
			}

			output, err := exec.Command("mount", "-o", "discard", snapshotLVPath, destPath).CombinedOutput()
			if err != nil {
				return fmt.Errorf("Error mounting snapshot LV: %v\noutput:'%s'", err, output)
			}

			if !c.isPrivileged() {
				return shiftRootfs(c, name, d)
			}

			return nil
		})

	} else if backing_fs == "btrfs" && shared.PathExists(fmt.Sprintf("%s.btrfs", shared.VarPath("images", hash))) {
		run = shared.OperationWrap(func() error {
			if err := btrfsCopyImage(hash, name, d); err != nil {
				return err
			}

			if !c.isPrivileged() {
				err = shiftRootfs(c, name, d)
				if err != nil {
					return err
				}
			}

			err = templateApply(c, "create")
			if err != nil {
				return err
			}

			return nil
		})

	} else {
		rootfsPath := fmt.Sprintf("%s/rootfs", dpath)
		err = os.MkdirAll(rootfsPath, 0700)
		if err != nil {
			return InternalError(fmt.Errorf("Error creating rootfs directory"))
		}

		run = shared.OperationWrap(func() error {
			if err := extractImage(hash, name, d); err != nil {
				return err
			}

			if !c.isPrivileged() {
				err = shiftRootfs(c, name, d)
				if err != nil {
					return err
				}
			}

			err = templateApply(c, "create")
			if err != nil {
				return err
			}

			return nil
		})
	}

	resources := make(map[string][]string)
	resources["containers"] = []string{req.Name}

	return &asyncResponse{run: run, resources: resources}
}

func createFromNone(d *Daemon, req *containerPostReq) Response {
	args := DbCreateContainerArgs{
		d:        d,
		name:     req.Name,
		ctype:    cTypeRegular,
		config:   req.Config,
		profiles: req.Profiles,
		ephem:    req.Ephemeral,
	}

	_, err := dbCreateContainer(args)
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

	createArgs := DbCreateContainerArgs{
		d:         d,
		name:      req.Name,
		ctype:     cTypeRegular,
		config:    req.Config,
		profiles:  req.Profiles,
		ephem:     req.Ephemeral,
		baseImage: req.Source.BaseImage,
	}

	_, err := dbCreateContainer(createArgs)
	if err != nil {
		return SmartError(err)
	}

	c, err := newLxdContainer(req.Name, d)
	if err != nil {
		removeContainer(d, req.Name)
		return SmartError(err)
	}

	// rsync complaisn if the parent directory for the rootfs sync doesn't
	// exist
	dpath := shared.VarPath("lxc", req.Name)
	if err := os.MkdirAll(dpath, 0700); err != nil {
		removeContainer(d, req.Name)
		return InternalError(err)
	}

	if err := extractShiftIfExists(d, c, req.Source.BaseImage, req.Name); err != nil {
		removeContainer(d, req.Name)
		return InternalError(err)
	}

	config, err := shared.GetTLSConfig(d.certf, d.keyf)
	if err != nil {
		removeContainer(d, req.Name)
		return InternalError(err)
	}

	args := migration.MigrationSinkArgs{
		Url:       req.Source.Operation,
		Dialer:    websocket.Dialer{TLSClientConfig: config},
		Container: c.c,
		Secrets:   req.Source.Websockets,
		IdMapSet:  c.idmapset,
	}

	sink, err := migration.NewMigrationSink(&args)
	if err != nil {
		removeContainer(d, req.Name)
		return BadRequest(err)
	}

	run := func() shared.OperationResult {
		err := sink()
		if err != nil {
			removeContainer(d, req.Name)
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

	args := DbCreateContainerArgs{
		d:         d,
		name:      req.Name,
		ctype:     cTypeRegular,
		config:    req.Config,
		profiles:  req.Profiles,
		ephem:     req.Ephemeral,
		baseImage: req.Source.BaseImage,
	}

	_, err = dbCreateContainer(args)
	if err != nil {
		return SmartError(err)
	}

	dpath := shared.VarPath("lxc", req.Name)
	if err := os.MkdirAll(dpath, 0700); err != nil {
		removeContainer(d, req.Name)
		return InternalError(err)
	}

	if err := extractShiftIfExists(d, source, req.Source.BaseImage, req.Name); err != nil {
		removeContainer(d, req.Name)
		return InternalError(err)
	}

	var oldPath string
	if shared.IsSnapshot(req.Source.Source) {
		snappieces := strings.SplitN(req.Source.Source, "/", 2)
		oldPath = migration.AddSlash(shared.VarPath("lxc",
			snappieces[0],
			"snapshots",
			snappieces[1],
			"rootfs"))
	} else {
		oldPath = migration.AddSlash(shared.VarPath("lxc", req.Source.Source, "rootfs"))
	}
	newPath := fmt.Sprintf("%s/%s", dpath, "rootfs")
	run := func() shared.OperationResult {
		output, err := exec.Command("rsync", "-a", "--devices", oldPath, newPath).CombinedOutput()

		if err == nil && !source.isPrivileged() {
			err = setUnprivUserAcl(source, dpath)
			if err != nil {
				shared.Debugf("Error adding acl for container root: falling back to chmod\n")
				output, err := exec.Command("chmod", "+x", dpath).CombinedOutput()
				if err != nil {
					shared.Debugf("Error chmoding the container root\n")
					shared.Debugf(string(output))
					return shared.OperationError(err)
				}
			}
		} else {
			shared.Debugf("rsync failed:\n%s", output)
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
