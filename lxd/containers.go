package main

import (
	"io/ioutil"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"

	log "github.com/lxc/lxd/shared/log15"
)

var containersCmd = APIEndpoint{
	Name: "containers",

	Get:  APIEndpointAction{Handler: containersGet, AccessHandler: AllowProjectPermission("containers", "view")},
	Post: APIEndpointAction{Handler: containersPost, AccessHandler: AllowProjectPermission("containers", "manage-containers")},
}

var containerCmd = APIEndpoint{
	Name: "containers/{name}",

	Get:    APIEndpointAction{Handler: containerGet, AccessHandler: AllowProjectPermission("containers", "view")},
	Put:    APIEndpointAction{Handler: containerPut, AccessHandler: AllowProjectPermission("containers", "manage-containers")},
	Delete: APIEndpointAction{Handler: containerDelete, AccessHandler: AllowProjectPermission("containers", "manage-containers")},
	Post:   APIEndpointAction{Handler: containerPost, AccessHandler: AllowProjectPermission("containers", "manage-containers")},
	Patch:  APIEndpointAction{Handler: containerPatch, AccessHandler: AllowProjectPermission("containers", "manage-containers")},
}

var containerStateCmd = APIEndpoint{
	Name: "containers/{name}/state",

	Get: APIEndpointAction{Handler: containerState, AccessHandler: AllowProjectPermission("containers", "view")},
	Put: APIEndpointAction{Handler: containerStatePut, AccessHandler: AllowProjectPermission("containers", "operate-containers")},
}

var containerFileCmd = APIEndpoint{
	Name: "containers/{name}/files",

	Get:    APIEndpointAction{Handler: containerFileHandler, AccessHandler: AllowProjectPermission("containers", "operate-containers")},
	Post:   APIEndpointAction{Handler: containerFileHandler, AccessHandler: AllowProjectPermission("containers", "operate-containers")},
	Delete: APIEndpointAction{Handler: containerFileHandler, AccessHandler: AllowProjectPermission("containers", "operate-containers")},
}

var containerSnapshotsCmd = APIEndpoint{
	Name: "containers/{name}/snapshots",

	Get:  APIEndpointAction{Handler: containerSnapshotsGet, AccessHandler: AllowProjectPermission("containers", "view")},
	Post: APIEndpointAction{Handler: containerSnapshotsPost, AccessHandler: AllowProjectPermission("containers", "operate-containers")},
}

var containerSnapshotCmd = APIEndpoint{
	Name: "containers/{name}/snapshots/{snapshotName}",

	Get:    APIEndpointAction{Handler: containerSnapshotHandler, AccessHandler: AllowProjectPermission("containers", "operate-containers")},
	Post:   APIEndpointAction{Handler: containerSnapshotHandler, AccessHandler: AllowProjectPermission("containers", "operate-containers")},
	Delete: APIEndpointAction{Handler: containerSnapshotHandler, AccessHandler: AllowProjectPermission("containers", "operate-containers")},
	Put:    APIEndpointAction{Handler: containerSnapshotHandler, AccessHandler: AllowProjectPermission("containers", "operate-containers")},
}

var containerConsoleCmd = APIEndpoint{
	Name: "containers/{name}/console",

	Get:    APIEndpointAction{Handler: containerConsoleLogGet, AccessHandler: AllowProjectPermission("containers", "view")},
	Post:   APIEndpointAction{Handler: containerConsolePost, AccessHandler: AllowProjectPermission("containers", "operate-containers")},
	Delete: APIEndpointAction{Handler: containerConsoleLogDelete, AccessHandler: AllowProjectPermission("containers", "operate-containers")},
}

var containerExecCmd = APIEndpoint{
	Name: "containers/{name}/exec",

	Post: APIEndpointAction{Handler: containerExecPost, AccessHandler: AllowProjectPermission("containers", "operate-containers")},
}

var containerMetadataCmd = APIEndpoint{
	Name: "containers/{name}/metadata",

	Get: APIEndpointAction{Handler: containerMetadataGet, AccessHandler: AllowProjectPermission("containers", "view")},
	Put: APIEndpointAction{Handler: containerMetadataPut, AccessHandler: AllowProjectPermission("containers", "manage-containers")},
}

var containerMetadataTemplatesCmd = APIEndpoint{
	Name: "containers/{name}/metadata/templates",

	Get:    APIEndpointAction{Handler: containerMetadataTemplatesGet, AccessHandler: AllowProjectPermission("containers", "view")},
	Post:   APIEndpointAction{Handler: containerMetadataTemplatesPostPut, AccessHandler: AllowProjectPermission("containers", "manage-containers")},
	Put:    APIEndpointAction{Handler: containerMetadataTemplatesPostPut, AccessHandler: AllowProjectPermission("containers", "manage-containers")},
	Delete: APIEndpointAction{Handler: containerMetadataTemplatesDelete, AccessHandler: AllowProjectPermission("containers", "manage-containers")},
}

var containerBackupsCmd = APIEndpoint{
	Name: "containers/{name}/backups",

	Get:  APIEndpointAction{Handler: containerBackupsGet, AccessHandler: AllowProjectPermission("containers", "view")},
	Post: APIEndpointAction{Handler: containerBackupsPost, AccessHandler: AllowProjectPermission("containers", "operate-containers")},
}

var containerBackupCmd = APIEndpoint{
	Name: "containers/{name}/backups/{backupName}",

	Get:    APIEndpointAction{Handler: containerBackupGet, AccessHandler: AllowProjectPermission("containers", "view")},
	Post:   APIEndpointAction{Handler: containerBackupPost, AccessHandler: AllowProjectPermission("containers", "operate-containers")},
	Delete: APIEndpointAction{Handler: containerBackupDelete, AccessHandler: AllowProjectPermission("containers", "operate-containers")},
}

var containerBackupExportCmd = APIEndpoint{
	Name: "containers/{name}/backups/{backupName}/export",

	Get: APIEndpointAction{Handler: containerBackupExportGet, AccessHandler: AllowProjectPermission("containers", "view")},
}

type containerAutostartList []container

func (slice containerAutostartList) Len() int {
	return len(slice)
}

func (slice containerAutostartList) Less(i, j int) bool {
	iOrder := slice[i].ExpandedConfig()["boot.autostart.priority"]
	jOrder := slice[j].ExpandedConfig()["boot.autostart.priority"]

	if iOrder != jOrder {
		iOrderInt, _ := strconv.Atoi(iOrder)
		jOrderInt, _ := strconv.Atoi(jOrder)
		return iOrderInt > jOrderInt
	}

	return slice[i].Name() < slice[j].Name()
}

func (slice containerAutostartList) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}

func containersRestart(s *state.State) error {
	// Get all the containers
	result, err := containerLoadNodeAll(s)
	if err != nil {
		return err
	}

	containers := []container{}

	for _, c := range result {
		containers = append(containers, c)
	}

	sort.Sort(containerAutostartList(containers))

	// Restart the containers
	for _, c := range containers {
		config := c.ExpandedConfig()
		lastState := config["volatile.last_state.power"]

		autoStart := config["boot.autostart"]
		autoStartDelay := config["boot.autostart.delay"]

		if shared.IsTrue(autoStart) || (autoStart == "" && lastState == "RUNNING") {
			if c.IsRunning() {
				continue
			}

			err = c.Start(false)
			if err != nil {
				logger.Errorf("Failed to start container '%s': %v", c.Name(), err)
			}

			autoStartDelayInt, err := strconv.Atoi(autoStartDelay)
			if err == nil {
				time.Sleep(time.Duration(autoStartDelayInt) * time.Second)
			}
		}
	}

	return nil
}

type containerStopList []container

func (slice containerStopList) Len() int {
	return len(slice)
}

func (slice containerStopList) Less(i, j int) bool {
	iOrder := slice[i].ExpandedConfig()["boot.stop.priority"]
	jOrder := slice[j].ExpandedConfig()["boot.stop.priority"]

	if iOrder != jOrder {
		iOrderInt, _ := strconv.Atoi(iOrder)
		jOrderInt, _ := strconv.Atoi(jOrder)
		return iOrderInt > jOrderInt // check this line (prob <)
	}

	return slice[i].Name() < slice[j].Name()
}

func (slice containerStopList) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}

// Return the names of all local containers, grouped by project. The
// information is obtained by reading the data directory.
func containersOnDisk() (map[string][]string, error) {
	containers := map[string][]string{}

	files, err := ioutil.ReadDir(shared.VarPath("containers"))
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		name := file.Name()
		project := "default"
		if strings.Contains(name, "_") {
			fields := strings.Split(file.Name(), "_")
			project = fields[0]
			name = fields[1]
		}
		names, ok := containers[project]
		if !ok {
			names = []string{}
		}
		containers[project] = append(names, name)
	}

	return containers, nil
}

func containersShutdown(s *state.State) error {
	var wg sync.WaitGroup

	dbAvailable := true

	// Get all the containers
	containers, err := containerLoadNodeAll(s)
	if err != nil {
		// Mark database as offline
		dbAvailable = false
		containers = []container{}

		// List all containers on disk
		cnames, err := containersOnDisk()
		if err != nil {
			return err
		}

		for project, names := range cnames {
			for _, name := range names {
				c, err := containerLXCLoad(s, db.ContainerArgs{
					Project: project,
					Name:    name,
					Config:  make(map[string]string),
				}, nil)
				if err != nil {
					return err
				}

				containers = append(containers, c)
			}
		}
	}

	sort.Sort(containerStopList(containers))

	if dbAvailable {
		// Reset all container states
		err = s.Cluster.ContainersResetState()
		if err != nil {
			return err
		}
	}

	var lastPriority int

	if len(containers) != 0 {
		lastPriority, _ = strconv.Atoi(containers[0].ExpandedConfig()["boot.stop.priority"])
	}

	for _, c := range containers {
		priority, _ := strconv.Atoi(c.ExpandedConfig()["boot.stop.priority"])

		// Enforce shutdown priority
		if priority != lastPriority {
			lastPriority = priority

			// Wait for containers with higher priority to finish
			wg.Wait()
		}

		// Record the current state
		lastState := c.State()

		// Stop the container
		if lastState != "BROKEN" && lastState != "STOPPED" {
			// Determinate how long to wait for the container to shutdown cleanly
			var timeoutSeconds int
			value, ok := c.ExpandedConfig()["boot.host_shutdown_timeout"]
			if ok {
				timeoutSeconds, _ = strconv.Atoi(value)
			} else {
				timeoutSeconds = 30
			}

			// Stop the container
			wg.Add(1)
			go func(c container, lastState string) {
				c.Shutdown(time.Second * time.Duration(timeoutSeconds))
				c.Stop(false)
				c.VolatileSet(map[string]string{"volatile.last_state.power": lastState})

				wg.Done()
			}(c, lastState)
		} else {
			c.VolatileSet(map[string]string{"volatile.last_state.power": lastState})
		}
	}
	wg.Wait()

	return nil
}

func containerDeleteSnapshots(s *state.State, project, cname string) error {
	results, err := s.Cluster.ContainerGetSnapshots(project, cname)
	if err != nil {
		return err
	}

	for _, sname := range results {
		sc, err := containerLoadByProjectAndName(s, project, sname)
		if err != nil {
			logger.Error(
				"containerDeleteSnapshots: Failed to load the snapshot container",
				log.Ctx{"container": cname, "snapshot": sname, "err": err})

			continue
		}

		if err := sc.Delete(); err != nil {
			logger.Error(
				"containerDeleteSnapshots: Failed to delete a snapshot container",
				log.Ctx{"container": cname, "snapshot": sname, "err": err})
		}
	}

	return nil
}
