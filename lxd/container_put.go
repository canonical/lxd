package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"
)

/*
 * Update configuration, or, if 'restore:snapshot-name' is present, restore
 * the named snapshot
 */
func containerPut(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := newLxdContainer(name, d)
	if err != nil {
		return NotFound
	}

	configRaw := containerConfigReq{}
	if err := json.NewDecoder(r.Body).Decode(&configRaw); err != nil {
		return BadRequest(err)
	}

	var do = func() error { return nil }

	if configRaw.Restore == "" {
		// Update container configuration
		do = func() error {
			return containerReplaceConfig(d, c, name, configRaw)
		}
	} else {
		// Snapshot Restore
		do = func() error {
			return containerSnapRestore(d, name, configRaw.Restore)
		}
	}

	return AsyncResponse(shared.OperationWrap(do), nil)
}

func containerReplaceConfig(d *Daemon, ct *lxdContainer, name string, newConfig containerConfigReq) error {
	/* check to see that the config actually applies to the container
	 * successfully before saving it. in particular, raw.lxc and
	 * raw.apparmor need to be parsed once to make sure they make sense.
	 */
	if err := ct.applyConfig(newConfig.Config, false); err != nil {
		return err
	}

	tx, err := dbBegin(d.db)
	if err != nil {
		return err
	}

	/* Update config or profiles */
	if err = dbClearContainerConfig(tx, ct.id); err != nil {
		shared.Debugf("Error clearing configuration for container %s\n", name)
		tx.Rollback()
		return err
	}

	if err = dbInsertContainerConfig(tx, ct.id, newConfig.Config); err != nil {
		shared.Debugf("Error inserting configuration for container %s\n", name)
		tx.Rollback()
		return err
	}

	/* handle profiles */
	if emptyProfile(newConfig.Profiles) {
		_, err := tx.Exec("DELETE from containers_profiles where container_id=?", ct.id)
		if err != nil {
			tx.Rollback()
			return err
		}
	} else {
		if err := dbInsertProfiles(tx, ct.id, newConfig.Profiles); err != nil {

			tx.Rollback()
			return err
		}
	}

	err = AddDevices(tx, "container", ct.id, newConfig.Devices)
	if err != nil {
		tx.Rollback()
		return err
	}

	return txCommit(tx)
}

func containerSnapRestore(d *Daemon, name string, snap string) error {
	// normalize snapshot name
	if !shared.IsSnapshot(snap) {
		snap = fmt.Sprintf("%s/%s", name, snap)
	}

	shared.Debugf("RESTORE => Restoring snapshot [%s] on container [%s]", snap, name)
	/*
	 * restore steps:
	 * 1. stop container if already running
	 * 2. overwrite existing config with snapshot config
	 * 3. copy snapshot rootfs to container
	 */
	wasRunning := false
	c, err := newLxdContainer(name, d)

	if err != nil {
		shared.Debugf("RESTORE => Error: newLxdContainer() failed for container", err)
		return err
	}

	// 1. stop container
	// TODO: stateful restore ?
	if c.c.Running() {
		wasRunning = true
		if err = c.Stop(); err != nil {
			shared.Debugf("RESTORE => Error: could not stop container", err)
			return err
		}
		shared.Debugf("RESTORE => Stopped container %s", name)
	}

	// 2, replace config

	// Make sure the source exists.
	source, err := newLxdContainer(snap, d)
	if err != nil {
		shared.Debugf("RESTORE => Error: newLxdContainer() failed for snapshot", err)
		return err
	}

	newConfig := containerConfigReq{}
	newConfig.Config = source.config
	newConfig.Profiles = source.profiles
	newConfig.Devices = source.devices

	err = containerReplaceConfig(d, c, name, newConfig)
	if err != nil {
		shared.Debugf("RESTORE => err #4", err)
		return err
	}

	// 3. copy rootfs
	// TODO: btrfs optimizations

	containerRootPath := shared.VarPath("lxc", name)

	if !shared.IsDir(path.Dir(containerRootPath)) {
		shared.Debugf("RESTORE => containerRoot [%s] directory does not exist", containerRootPath)
		return os.ErrNotExist
	}

	var snapshotRootFSPath string
	snapshotRootFSPath = shared.AddSlash(snapshotRootfsDir(c, strings.SplitN(snap, "/", 2)[1]))

	containerRootFSPath := shared.AddSlash(fmt.Sprintf("%s/%s", containerRootPath, "rootfs"))
	shared.Debugf("RESTORE => Copying %s to %s", snapshotRootFSPath, containerRootFSPath)

	rsyncVerbosity := "-q"
	if *debug {
		rsyncVerbosity = "-vi"
	}

	output, err := exec.Command("rsync", "-a", "-c", "-HAX", "--devices", "--delete", rsyncVerbosity, snapshotRootFSPath, containerRootFSPath).CombinedOutput()
	shared.Debugf("RESTORE => rsync output\n%s", output)

	if err == nil && !source.isPrivileged() {
		err = setUnprivUserAcl(c, containerRootPath)
		if err != nil {
			shared.Debugf("Error adding acl for container root: falling back to chmod\n")
			output, err := exec.Command("chmod", "+x", containerRootPath).CombinedOutput()
			if err != nil {
				shared.Debugf("Error chmoding the container root\n")
				shared.Debugf(string(output))
				return err
			}
		}
	} else {
		shared.Debugf("rsync failed:\n%s", output)
		return err
	}

	if wasRunning {
		c.Start()
	}

	return nil
}

func emptyProfile(l []string) bool {
	if len(l) == 0 {
		return true
	}
	if len(l) == 1 && l[0] == "" {
		return true
	}
	return false
}
