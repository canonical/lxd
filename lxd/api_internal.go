package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/osarch"

	log "gopkg.in/inconshreveable/log15.v2"
)

var apiInternal = []Command{
	internalReadyCmd,
	internalShutdownCmd,
	internalContainerOnStartCmd,
	internalContainerOnStopCmd,
	internalContainersCmd,
}

func internalReady(d *Daemon, r *http.Request) Response {
	if !d.SetupMode {
		return InternalError(fmt.Errorf("The server isn't currently in setup mode"))
	}

	err := d.Ready()
	if err != nil {
		return InternalError(err)
	}

	d.SetupMode = false

	return EmptySyncResponse
}

func internalWaitReady(d *Daemon, r *http.Request) Response {
	<-d.readyChan

	return EmptySyncResponse
}

func internalShutdown(d *Daemon, r *http.Request) Response {
	d.shutdownChan <- true

	return EmptySyncResponse
}

func internalContainerOnStart(d *Daemon, r *http.Request) Response {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		return SmartError(err)
	}

	c, err := containerLoadById(d, id)
	if err != nil {
		return SmartError(err)
	}

	err = c.OnStart()
	if err != nil {
		shared.Log.Error("start hook failed", log.Ctx{"container": c.Name(), "err": err})
		return SmartError(err)
	}

	return EmptySyncResponse
}

func internalContainerOnStop(d *Daemon, r *http.Request) Response {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		return SmartError(err)
	}

	target := r.FormValue("target")
	if target == "" {
		target = "unknown"
	}

	c, err := containerLoadById(d, id)
	if err != nil {
		return SmartError(err)
	}

	err = c.OnStop(target)
	if err != nil {
		shared.Log.Error("stop hook failed", log.Ctx{"container": c.Name(), "err": err})
		return SmartError(err)
	}

	return EmptySyncResponse
}

var internalShutdownCmd = Command{name: "shutdown", put: internalShutdown}
var internalReadyCmd = Command{name: "ready", put: internalReady, get: internalWaitReady}
var internalContainerOnStartCmd = Command{name: "containers/{id}/onstart", get: internalContainerOnStart}
var internalContainerOnStopCmd = Command{name: "containers/{id}/onstop", get: internalContainerOnStop}

func slurpBackupFile(path string) (*backupFile, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	sf := backupFile{}

	if err := yaml.Unmarshal(data, &sf); err != nil {
		return nil, err
	}

	return &sf, nil
}

func internalImport(d *Daemon, r *http.Request) Response {
	name := r.FormValue("target")
	if name == "" {
		return BadRequest(fmt.Errorf("target is required"))
	}

	// The following code likely requires some explanation. When an existing
	// container accidently got removed from the containers database || the
	// storage volumes database and the storage volume for for the container
	// got unmounted but we want to import it again, we need to
	// detect the storage information for the container:
	// - check if the symlink of the container points to a valid mountpoint.
	// - If it does not read the link and parse out the pool the container
	//   existed on.
	// - Check the driver of the storage pool the container existed on and
	//   if it is an appropriate driver, do something smart on how to
	//   temporarily remount the containers storage volume to retrieve the
	//   backup.yaml file for it.
	path := containerPath(name, false)
	containerMntPoint, err := os.Readlink(path)
	if err != nil {
		return InternalError(err)
	}

	if !shared.IsMountPoint(path) {
		poolIdx := strings.Index(containerMntPoint, "/storage-pools/")
		containerSubString := fmt.Sprintf("/containers/%s", name)
		containersIdx := strings.Index(containerMntPoint, containerSubString)
		if (poolIdx < 0) || (containersIdx < 0) || (containersIdx <= poolIdx) {
			return InternalError(fmt.Errorf("Symlink does not point to a valid container mountpoint."))
		}
		poolName := containerMntPoint[poolIdx+len("/storage-pools/") : containersIdx]
		if strings.Contains(poolName, "/") {
			return InternalError(fmt.Errorf("Symlink target does not contain a valid storage pool name."))
		}

		_, pool, err := dbStoragePoolGet(d.db, poolName)
		if err != nil {
			return SmartError(err)
		}

		switch pool.Driver {
		case "zfs":
			err = zfsMount(poolName, containerSubString[1:])
			if err != nil {
				return InternalError(err)
			}
			defer zfsUmount(poolName, containerSubString[1:])
		case "lvm":
			containerLvmName := containerNameToLVName(name)
			containerLvmPath := getLvmDevPath(poolName, storagePoolVolumeApiEndpointContainers, containerLvmName)
			err := tryMount(containerLvmPath, containerMntPoint, "", 0, "")
			if err != nil {
				return InternalError(err)
			}
			defer tryUnmount(containerLvmPath, 0)
		case "dir":
			// noop: There is nothing to mount.
		case "btrfs":
			// noop: The btrfs storage pool should never be
			// unmounted.
		}
	}

	sf, err := slurpBackupFile(shared.VarPath("containers", name, "backup.yaml"))
	if err != nil {
		return SmartError(err)
	}

	baseImage := sf.Container.Config["volatile.base_image"]
	for k := range sf.Container.Config {
		if strings.HasPrefix(k, "volatile") {
			delete(sf.Container.Config, k)
		}
	}

	arch, err := osarch.ArchitectureId(sf.Container.Architecture)
	if err != nil {
		return SmartError(err)
	}
	_, err = containerCreateInternal(d, containerArgs{
		Architecture: arch,
		BaseImage:    baseImage,
		Config:       sf.Container.Config,
		CreationDate: sf.Container.CreatedAt,
		LastUsedDate: sf.Container.LastUsedAt,
		Ctype:        cTypeRegular,
		Devices:      sf.Container.Devices,
		Ephemeral:    sf.Container.Ephemeral,
		Name:         sf.Container.Name,
		Profiles:     sf.Container.Profiles,
		Stateful:     sf.Container.Stateful,
	})
	if err != nil {
		return SmartError(err)
	}

	for _, snap := range sf.Snapshots {
		baseImage := snap.Config["volatile.base_image"]
		for k := range snap.Config {
			if strings.HasPrefix(k, "volatile") {
				delete(snap.Config, k)
			}
		}

		arch, err := osarch.ArchitectureId(snap.Architecture)
		if err != nil {
			return SmartError(err)
		}

		_, err = containerCreateInternal(d, containerArgs{
			Architecture: arch,
			BaseImage:    baseImage,
			Config:       snap.Config,
			CreationDate: snap.CreationDate,
			LastUsedDate: snap.LastUsedDate,
			Ctype:        cTypeSnapshot,
			Devices:      snap.Devices,
			Ephemeral:    snap.Ephemeral,
			Name:         snap.Name,
			Profiles:     snap.Profiles,
			Stateful:     snap.Stateful,
		})
		if err != nil {
			return SmartError(err)
		}
	}

	return EmptySyncResponse
}

var internalContainersCmd = Command{name: "containers", post: internalImport}
