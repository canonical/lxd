package main

import (
	"fmt"
	"io/ioutil"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/warnings"
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
)

var instancesCmd = APIEndpoint{
	Name: "instances",
	Path: "instances",
	Aliases: []APIEndpointAlias{
		{Name: "containers", Path: "containers"},
		{Name: "vms", Path: "virtual-machines"},
	},

	Get:  APIEndpointAction{Handler: instancesGet, AccessHandler: allowProjectPermission("containers", "view")},
	Post: APIEndpointAction{Handler: instancesPost, AccessHandler: allowProjectPermission("containers", "manage-containers")},
	Put:  APIEndpointAction{Handler: instancesPut, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceCmd = APIEndpoint{
	Name: "instance",
	Path: "instances/{name}",
	Aliases: []APIEndpointAlias{
		{Name: "container", Path: "containers/{name}"},
		{Name: "vm", Path: "virtual-machines/{name}"},
	},

	Get:    APIEndpointAction{Handler: instanceGet, AccessHandler: allowProjectPermission("containers", "view")},
	Put:    APIEndpointAction{Handler: instancePut, AccessHandler: allowProjectPermission("containers", "manage-containers")},
	Delete: APIEndpointAction{Handler: instanceDelete, AccessHandler: allowProjectPermission("containers", "manage-containers")},
	Post:   APIEndpointAction{Handler: instancePost, AccessHandler: allowProjectPermission("containers", "manage-containers")},
	Patch:  APIEndpointAction{Handler: instancePatch, AccessHandler: allowProjectPermission("containers", "manage-containers")},
}

var instanceStateCmd = APIEndpoint{
	Name: "instanceState",
	Path: "instances/{name}/state",
	Aliases: []APIEndpointAlias{
		{Name: "containerState", Path: "containers/{name}/state"},
		{Name: "vmState", Path: "virtual-machines/{name}/state"},
	},

	Get: APIEndpointAction{Handler: instanceState, AccessHandler: allowProjectPermission("containers", "view")},
	Put: APIEndpointAction{Handler: instanceStatePut, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceFileCmd = APIEndpoint{
	Name: "instanceFile",
	Path: "instances/{name}/files",
	Aliases: []APIEndpointAlias{
		{Name: "containerFile", Path: "containers/{name}/files"},
		{Name: "vmFile", Path: "virtual-machines/{name}/files"},
	},

	Get:    APIEndpointAction{Handler: instanceFileHandler, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Post:   APIEndpointAction{Handler: instanceFileHandler, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Delete: APIEndpointAction{Handler: instanceFileHandler, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceSnapshotsCmd = APIEndpoint{
	Name: "instanceSnapshots",
	Path: "instances/{name}/snapshots",
	Aliases: []APIEndpointAlias{
		{Name: "containerSnapshots", Path: "containers/{name}/snapshots"},
		{Name: "vmSnapshots", Path: "virtual-machines/{name}/snapshots"},
	},

	Get:  APIEndpointAction{Handler: instanceSnapshotsGet, AccessHandler: allowProjectPermission("containers", "view")},
	Post: APIEndpointAction{Handler: instanceSnapshotsPost, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceSnapshotCmd = APIEndpoint{
	Name: "instanceSnapshot",
	Path: "instances/{name}/snapshots/{snapshotName}",
	Aliases: []APIEndpointAlias{
		{Name: "containerSnapshot", Path: "containers/{name}/snapshots/{snapshotName}"},
		{Name: "vmSnapshot", Path: "virtual-machines/{name}/snapshots/{snapshotName}"},
	},

	Get:    APIEndpointAction{Handler: instanceSnapshotHandler, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Post:   APIEndpointAction{Handler: instanceSnapshotHandler, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Delete: APIEndpointAction{Handler: instanceSnapshotHandler, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Patch:  APIEndpointAction{Handler: instanceSnapshotHandler, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Put:    APIEndpointAction{Handler: instanceSnapshotHandler, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceConsoleCmd = APIEndpoint{
	Name: "instanceConsole",
	Path: "instances/{name}/console",
	Aliases: []APIEndpointAlias{
		{Name: "containerConsole", Path: "containers/{name}/console"},
		{Name: "vmConsole", Path: "virtual-machines/{name}/console"},
	},

	Get:    APIEndpointAction{Handler: instanceConsoleLogGet, AccessHandler: allowProjectPermission("containers", "view")},
	Post:   APIEndpointAction{Handler: instanceConsolePost, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Delete: APIEndpointAction{Handler: instanceConsoleLogDelete, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceExecCmd = APIEndpoint{
	Name: "instanceExec",
	Path: "instances/{name}/exec",
	Aliases: []APIEndpointAlias{
		{Name: "containerExec", Path: "containers/{name}/exec"},
		{Name: "vmExec", Path: "virtual-machines/{name}/exec"},
	},

	Post: APIEndpointAction{Handler: instanceExecPost, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceMetadataCmd = APIEndpoint{
	Name: "instanceMetadata",
	Path: "instances/{name}/metadata",
	Aliases: []APIEndpointAlias{
		{Name: "containerMetadata", Path: "containers/{name}/metadata"},
		{Name: "vmMetadata", Path: "virtual-machines/{name}/metadata"},
	},

	Get:   APIEndpointAction{Handler: instanceMetadataGet, AccessHandler: allowProjectPermission("containers", "view")},
	Patch: APIEndpointAction{Handler: instanceMetadataPatch, AccessHandler: allowProjectPermission("containers", "manage-containers")},
	Put:   APIEndpointAction{Handler: instanceMetadataPut, AccessHandler: allowProjectPermission("containers", "manage-containers")},
}

var instanceMetadataTemplatesCmd = APIEndpoint{
	Name: "instanceMetadataTemplates",
	Path: "instances/{name}/metadata/templates",
	Aliases: []APIEndpointAlias{
		{Name: "containerMetadataTemplates", Path: "containers/{name}/metadata/templates"},
		{Name: "vmMetadataTemplates", Path: "virtual-machines/{name}/metadata/templates"},
	},

	Get:    APIEndpointAction{Handler: instanceMetadataTemplatesGet, AccessHandler: allowProjectPermission("containers", "view")},
	Post:   APIEndpointAction{Handler: instanceMetadataTemplatesPost, AccessHandler: allowProjectPermission("containers", "manage-containers")},
	Delete: APIEndpointAction{Handler: instanceMetadataTemplatesDelete, AccessHandler: allowProjectPermission("containers", "manage-containers")},
}

var instanceBackupsCmd = APIEndpoint{
	Name: "instanceBackups",
	Path: "instances/{name}/backups",
	Aliases: []APIEndpointAlias{
		{Name: "containerBackups", Path: "containers/{name}/backups"},
		{Name: "vmBackups", Path: "virtual-machines/{name}/backups"},
	},

	Get:  APIEndpointAction{Handler: instanceBackupsGet, AccessHandler: allowProjectPermission("containers", "view")},
	Post: APIEndpointAction{Handler: instanceBackupsPost, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceBackupCmd = APIEndpoint{
	Name: "instanceBackup",
	Path: "instances/{name}/backups/{backupName}",
	Aliases: []APIEndpointAlias{
		{Name: "containerBackup", Path: "containers/{name}/backups/{backupName}"},
		{Name: "vmBackup", Path: "virtual-machines/{name}/backups/{backupName}"},
	},

	Get:    APIEndpointAction{Handler: instanceBackupGet, AccessHandler: allowProjectPermission("containers", "view")},
	Post:   APIEndpointAction{Handler: instanceBackupPost, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Delete: APIEndpointAction{Handler: instanceBackupDelete, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceBackupExportCmd = APIEndpoint{
	Name: "instanceBackupExport",
	Path: "instances/{name}/backups/{backupName}/export",
	Aliases: []APIEndpointAlias{
		{Name: "containerBackupExport", Path: "containers/{name}/backups/{backupName}/export"},
		{Name: "vmBackupExport", Path: "virtual-machines/{name}/backups/{backupName}/export"},
	},

	Get: APIEndpointAction{Handler: instanceBackupExportGet, AccessHandler: allowProjectPermission("containers", "view")},
}

type instanceAutostartList []instance.Instance

func (slice instanceAutostartList) Len() int {
	return len(slice)
}

func (slice instanceAutostartList) Less(i, j int) bool {
	iOrder := slice[i].ExpandedConfig()["boot.autostart.priority"]
	jOrder := slice[j].ExpandedConfig()["boot.autostart.priority"]

	if iOrder != jOrder {
		iOrderInt, _ := strconv.Atoi(iOrder)
		jOrderInt, _ := strconv.Atoi(jOrder)
		return iOrderInt > jOrderInt
	}

	return slice[i].Name() < slice[j].Name()
}

func (slice instanceAutostartList) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}

func instancesStart(s *state.State, instances []instance.Instance) {
	sort.Sort(instanceAutostartList(instances))

	maxAttempts := 3

	// Restart the instances
	for _, inst := range instances {
		// Get the instance config.
		config := inst.ExpandedConfig()
		lastState := config["volatile.last_state.power"]
		autoStart := config["boot.autostart"]
		autoStartDelay := config["boot.autostart.delay"]

		// Only restart instances configured to auto-start or that were previously running.
		start := shared.IsTrue(autoStart) || (autoStart == "" && lastState == "RUNNING")
		if !start {
			continue
		}

		// If already running, we're done.
		if inst.IsRunning() {
			continue
		}

		instLogger := logging.AddContext(logger.Log, log.Ctx{"project": inst.Project(), "instance": inst.Name()})

		// Try to start the instance.
		var err error
		var attempt = 0
		for {
			attempt++
			err = inst.Start(false)
			if err != nil {
				instLogger.Warn("Failed auto start instance attempt", log.Ctx{"attempt": attempt, "maxAttempts": maxAttempts, "err": err})

				if attempt >= maxAttempts {
					break
				}

				time.Sleep(5 * time.Second)
			} else {
				// Resolve any previous warning.
				warnErr := warnings.ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(s.Cluster, inst.Project(), db.WarningInstanceAutostartFailure, cluster.TypeInstance, inst.ID())
				if warnErr != nil {
					instLogger.Warn("Failed to resolve instance autostart failure warning", log.Ctx{"err": warnErr})
				}

				break
			}
		}

		if err != nil {
			// If unable to start after 3 tries, record a warning.
			warnErr := s.Cluster.UpsertWarningLocalNode(inst.Project(), cluster.TypeInstance, inst.ID(), db.WarningInstanceAutostartFailure, fmt.Sprintf("%v", err))
			if warnErr != nil {
				instLogger.Warn("Failed to create instance autostart failure warning", log.Ctx{"err": warnErr})
			}

			instLogger.Error("Failed to auto start instance", log.Ctx{"err": err})
			continue
		}

		// Wait the auto-start delay if set.
		autoStartDelayInt, err := strconv.Atoi(autoStartDelay)
		if err == nil {
			time.Sleep(time.Duration(autoStartDelayInt) * time.Second)
		}
	}

	return
}

type instanceStopList []instance.Instance

func (slice instanceStopList) Len() int {
	return len(slice)
}

func (slice instanceStopList) Less(i, j int) bool {
	iOrder := slice[i].ExpandedConfig()["boot.stop.priority"]
	jOrder := slice[j].ExpandedConfig()["boot.stop.priority"]

	if iOrder != jOrder {
		iOrderInt, _ := strconv.Atoi(iOrder)
		jOrderInt, _ := strconv.Atoi(jOrder)
		return iOrderInt > jOrderInt // check this line (prob <)
	}

	return slice[i].Name() < slice[j].Name()
}

func (slice instanceStopList) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}

// Return the names of all local instances, grouped by project. The
// information is obtained by reading the data directory.
func instancesOnDisk() (map[string][]string, error) {
	instances := map[string][]string{}

	containers, err := ioutil.ReadDir(shared.VarPath("containers"))
	if err != nil {
		return nil, err
	}

	virtualMachines, err := ioutil.ReadDir(shared.VarPath("virtual-machines"))
	if err != nil {
		return nil, err
	}

	for _, file := range append(containers, virtualMachines...) {
		name := file.Name()
		projectName := project.Default
		if strings.Contains(name, "_") {
			fields := strings.Split(file.Name(), "_")
			projectName = fields[0]
			name = fields[1]
		}
		names, ok := instances[projectName]
		if !ok {
			names = []string{}
		}
		instances[projectName] = append(names, name)
	}

	return instances, nil
}

// instancesShutdown shuts down the supplied instances if they are running.
// If instances is nil (not empty) then it will consider the database to be unavailable and will not attempt to
// update instance power state records.
func instancesShutdown(s *state.State, instances []instance.Instance) error {
	var wg sync.WaitGroup

	dbAvailable := instances != nil

	sort.Sort(instanceStopList(instances))

	if dbAvailable {
		// Reset all instances states
		err := s.Cluster.ResetInstancesPowerState()
		if err != nil {
			return err
		}
	}

	var lastPriority int

	if len(instances) != 0 {
		lastPriority, _ = strconv.Atoi(instances[0].ExpandedConfig()["boot.stop.priority"])
	}

	for _, inst := range instances {
		priority, _ := strconv.Atoi(inst.ExpandedConfig()["boot.stop.priority"])

		// Enforce shutdown priority
		if priority != lastPriority {
			lastPriority = priority

			// Wait for instances with higher priority to finish
			wg.Wait()
		}

		// Record the current state
		lastState := inst.State()

		// Stop the container
		if lastState != "ERROR" && lastState != "STOPPED" {
			// Determinate how long to wait for the instance to shutdown cleanly
			var timeoutSeconds int
			value, ok := inst.ExpandedConfig()["boot.host_shutdown_timeout"]
			if ok {
				timeoutSeconds, _ = strconv.Atoi(value)
			} else {
				timeoutSeconds = 30
			}

			// Stop the instance
			wg.Add(1)
			go func(inst instance.Instance, lastState string) {
				if inst.IsRunning() {
					inst.Shutdown(time.Second * time.Duration(timeoutSeconds))
					inst.Stop(false)
				}

				if dbAvailable {
					inst.VolatileSet(map[string]string{"volatile.last_state.power": lastState})
				}

				wg.Done()
			}(inst, lastState)
		} else if dbAvailable {
			inst.VolatileSet(map[string]string{"volatile.last_state.power": lastState})
		}
	}
	wg.Wait()

	return nil
}
