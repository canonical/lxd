package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxd/response"
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

func internalReady(d *Daemon, r *http.Request) response.Response {
	if !d.SetupMode {
		return response.InternalError(fmt.Errorf("The server isn't currently in setup mode"))
	}

	err := d.Ready()
	if err != nil {
		return response.InternalError(err)
	}

	d.SetupMode = false

	return response.EmptySyncResponse
}

func internalWaitReady(d *Daemon, r *http.Request) response.Response {
	<-d.readyChan

	return response.EmptySyncResponse
}

func internalShutdown(d *Daemon, r *http.Request) response.Response {
	d.shutdownChan <- true

	return response.EmptySyncResponse
}

func internalContainerOnStart(d *Daemon, r *http.Request) response.Response {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	c, err := containerLoadById(d, id)
	if err != nil {
		return response.SmartError(err)
	}

	err = c.OnStart()
	if err != nil {
		shared.Log.Error("start hook failed", log.Ctx{"container": c.Name(), "err": err})
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalContainerOnStop(d *Daemon, r *http.Request) response.Response {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	target := r.FormValue("target")
	if target == "" {
		target = "unknown"
	}

	c, err := containerLoadById(d, id)
	if err != nil {
		return response.SmartError(err)
	}

	err = c.OnStop(target)
	if err != nil {
		shared.Log.Error("stop hook failed", log.Ctx{"container": c.Name(), "err": err})
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
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

func internalImport(d *Daemon, r *http.Request) response.Response {
	name := r.FormValue("target")
	if name == "" {
		return response.BadRequest(fmt.Errorf("target is required"))
	}

	path := containerPath(name, false)
	err := d.Storage.ContainerStart(name, path)
	if err != nil {
		return response.SmartError(err)
	}

	defer d.Storage.ContainerStop(name, path)

	sf, err := slurpBackupFile(shared.VarPath("containers", name, "backup.yaml"))
	if err != nil {
		return response.SmartError(err)
	}

	baseImage := sf.Container.Config["volatile.base_image"]
	for k := range sf.Container.Config {
		if strings.HasPrefix(k, "volatile") {
			delete(sf.Container.Config, k)
		}
	}

	arch, err := osarch.ArchitectureId(sf.Container.Architecture)
	if err != nil {
		return response.SmartError(err)
	}
	_, err = containerCreateInternal(d, containerArgs{
		Architecture: arch,
		BaseImage:    baseImage,
		Config:       sf.Container.Config,
		CreationDate: sf.Container.CreationDate,
		LastUsedDate: sf.Container.LastUsedDate,
		Ctype:        cTypeRegular,
		Devices:      sf.Container.Devices,
		Ephemeral:    sf.Container.Ephemeral,
		Name:         sf.Container.Name,
		Profiles:     sf.Container.Profiles,
		Stateful:     sf.Container.Stateful,
	})
	if err != nil {
		return response.SmartError(err)
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
			return response.SmartError(err)
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
			return response.SmartError(err)
		}
	}

	return response.EmptySyncResponse
}

var internalContainersCmd = Command{name: "containers", post: internalImport}
