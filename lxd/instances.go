package main

import (
	"io/ioutil"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/logger"
)

var instancesCmd = APIEndpoint{
	Name: "instances",
	Path: "instances",
	Aliases: []APIEndpointAlias{
		{Name: "containers", Path: "containers"},
		{Name: "vms", Path: "virtual-machines"},
	},

	Get:  APIEndpointAction{Handler: containersGet, AccessHandler: allowProjectPermission("containers", "view")},
	Post: APIEndpointAction{Handler: containersPost, AccessHandler: allowProjectPermission("containers", "manage-containers")},
	Put:  APIEndpointAction{Handler: containersPut, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceCmd = APIEndpoint{
	Name: "instance",
	Path: "instances/{name}",
	Aliases: []APIEndpointAlias{
		{Name: "container", Path: "containers/{name}"},
		{Name: "vm", Path: "virtual-machines/{name}"},
	},

	Get:    APIEndpointAction{Handler: containerGet, AccessHandler: allowProjectPermission("containers", "view")},
	Put:    APIEndpointAction{Handler: containerPut, AccessHandler: allowProjectPermission("containers", "manage-containers")},
	Delete: APIEndpointAction{Handler: containerDelete, AccessHandler: allowProjectPermission("containers", "manage-containers")},
	Post:   APIEndpointAction{Handler: containerPost, AccessHandler: allowProjectPermission("containers", "manage-containers")},
	Patch:  APIEndpointAction{Handler: containerPatch, AccessHandler: allowProjectPermission("containers", "manage-containers")},
}

var instanceStateCmd = APIEndpoint{
	Name: "instanceState",
	Path: "instances/{name}/state",
	Aliases: []APIEndpointAlias{
		{Name: "containerState", Path: "containers/{name}/state"},
		{Name: "vmState", Path: "virtual-machines/{name}/state"},
	},

	Get: APIEndpointAction{Handler: containerState, AccessHandler: allowProjectPermission("containers", "view")},
	Put: APIEndpointAction{Handler: containerStatePut, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceFileCmd = APIEndpoint{
	Name: "instanceFile",
	Path: "instances/{name}/files",
	Aliases: []APIEndpointAlias{
		{Name: "containerFile", Path: "containers/{name}/files"},
		{Name: "vmFile", Path: "virtual-machines/{name}/files"},
	},

	Get:    APIEndpointAction{Handler: containerFileHandler, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Post:   APIEndpointAction{Handler: containerFileHandler, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Delete: APIEndpointAction{Handler: containerFileHandler, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceSnapshotsCmd = APIEndpoint{
	Name: "instanceSnapshots",
	Path: "instances/{name}/snapshots",
	Aliases: []APIEndpointAlias{
		{Name: "containerSnapshots", Path: "containers/{name}/snapshots"},
		{Name: "vmSnapshots", Path: "virtual-machines/{name}/snapshots"},
	},

	Get:  APIEndpointAction{Handler: containerSnapshotsGet, AccessHandler: allowProjectPermission("containers", "view")},
	Post: APIEndpointAction{Handler: containerSnapshotsPost, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceSnapshotCmd = APIEndpoint{
	Name: "instanceSnapshot",
	Path: "instances/{name}/snapshots/{snapshotName}",
	Aliases: []APIEndpointAlias{
		{Name: "containerSnapshot", Path: "containers/{name}/snapshots/{snapshotName}"},
		{Name: "vmSnapshot", Path: "virtual-machines/{name}/snapshots/{snapshotName}"},
	},

	Get:    APIEndpointAction{Handler: containerSnapshotHandler, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Post:   APIEndpointAction{Handler: containerSnapshotHandler, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Delete: APIEndpointAction{Handler: containerSnapshotHandler, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Put:    APIEndpointAction{Handler: containerSnapshotHandler, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceConsoleCmd = APIEndpoint{
	Name: "instanceConsole",
	Path: "instances/{name}/console",
	Aliases: []APIEndpointAlias{
		{Name: "containerConsole", Path: "containers/{name}/console"},
		{Name: "vmConsole", Path: "virtual-machines/{name}/console"},
	},

	Get:    APIEndpointAction{Handler: containerConsoleLogGet, AccessHandler: allowProjectPermission("containers", "view")},
	Post:   APIEndpointAction{Handler: containerConsolePost, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Delete: APIEndpointAction{Handler: containerConsoleLogDelete, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceExecCmd = APIEndpoint{
	Name: "instanceExec",
	Path: "instances/{name}/exec",
	Aliases: []APIEndpointAlias{
		{Name: "containerExec", Path: "containers/{name}/exec"},
		{Name: "vmExec", Path: "virtual-machines/{name}/exec"},
	},

	Post: APIEndpointAction{Handler: containerExecPost, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceMetadataCmd = APIEndpoint{
	Name: "instanceMetadata",
	Path: "instances/{name}/metadata",
	Aliases: []APIEndpointAlias{
		{Name: "containerMetadata", Path: "containers/{name}/metadata"},
		{Name: "vmMetadata", Path: "virtual-machines/{name}/metadata"},
	},

	Get: APIEndpointAction{Handler: containerMetadataGet, AccessHandler: allowProjectPermission("containers", "view")},
	Put: APIEndpointAction{Handler: containerMetadataPut, AccessHandler: allowProjectPermission("containers", "manage-containers")},
}

var instanceMetadataTemplatesCmd = APIEndpoint{
	Name: "instanceMetadataTemplates",
	Path: "instances/{name}/metadata/templates",
	Aliases: []APIEndpointAlias{
		{Name: "containerMetadataTemplates", Path: "containers/{name}/metadata/templates"},
		{Name: "vmMetadataTemplates", Path: "virtual-machines/{name}/metadata/templates"},
	},

	Get:    APIEndpointAction{Handler: containerMetadataTemplatesGet, AccessHandler: allowProjectPermission("containers", "view")},
	Post:   APIEndpointAction{Handler: containerMetadataTemplatesPostPut, AccessHandler: allowProjectPermission("containers", "manage-containers")},
	Put:    APIEndpointAction{Handler: containerMetadataTemplatesPostPut, AccessHandler: allowProjectPermission("containers", "manage-containers")},
	Delete: APIEndpointAction{Handler: containerMetadataTemplatesDelete, AccessHandler: allowProjectPermission("containers", "manage-containers")},
}

var instanceBackupsCmd = APIEndpoint{
	Name: "instanceBackups",
	Path: "instances/{name}/backups",
	Aliases: []APIEndpointAlias{
		{Name: "containerBackups", Path: "containers/{name}/backups"},
		{Name: "vmBackups", Path: "virtual-machines/{name}/backups"},
	},

	Get:  APIEndpointAction{Handler: containerBackupsGet, AccessHandler: allowProjectPermission("containers", "view")},
	Post: APIEndpointAction{Handler: containerBackupsPost, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceBackupCmd = APIEndpoint{
	Name: "instanceBackup",
	Path: "instances/{name}/backups/{backupName}",
	Aliases: []APIEndpointAlias{
		{Name: "containerBackup", Path: "containers/{name}/backups/{backupName}"},
		{Name: "vmBackup", Path: "virtual-machines/{name}/backups/{backupName}"},
	},

	Get:    APIEndpointAction{Handler: containerBackupGet, AccessHandler: allowProjectPermission("containers", "view")},
	Post:   APIEndpointAction{Handler: containerBackupPost, AccessHandler: allowProjectPermission("containers", "operate-containers")},
	Delete: APIEndpointAction{Handler: containerBackupDelete, AccessHandler: allowProjectPermission("containers", "operate-containers")},
}

var instanceBackupExportCmd = APIEndpoint{
	Name: "instanceBackupExport",
	Path: "instances/{name}/backups/{backupName}/export",
	Aliases: []APIEndpointAlias{
		{Name: "containerBackupExport", Path: "containers/{name}/backups/{backupName}/export"},
		{Name: "vmBackupExport", Path: "virtual-machines/{name}/backups/{backupName}/export"},
	},

	Get: APIEndpointAction{Handler: containerBackupExportGet, AccessHandler: allowProjectPermission("containers", "view")},
}

type containerAutostartList []instance.Instance

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
	// Get all the instances
	result, err := instance.LoadNodeAll(s, instancetype.Any)
	if err != nil {
		return err
	}

	instances := []instance.Instance{}

	for _, c := range result {
		instances = append(instances, c)
	}

	sort.Sort(containerAutostartList(instances))

	// Restart the instances
	for _, c := range instances {
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
				logger.Errorf("Failed to start instance '%s': %v", c.Name(), err)
			}

			autoStartDelayInt, err := strconv.Atoi(autoStartDelay)
			if err == nil {
				time.Sleep(time.Duration(autoStartDelayInt) * time.Second)
			}
		}
	}

	return nil
}

func vmMonitor(s *state.State) error {
	// Get all the instances
	insts, err := instance.LoadNodeAll(s, instancetype.VM)
	if err != nil {
		return err
	}

	for _, inst := range insts {
		// Retrieve running state, this will re-connect to QMP
		inst.IsRunning()
	}

	return nil
}

type containerStopList []instance.Instance

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
		projectName := project.Default
		if strings.Contains(name, "_") {
			fields := strings.Split(file.Name(), "_")
			projectName = fields[0]
			name = fields[1]
		}
		names, ok := containers[projectName]
		if !ok {
			names = []string{}
		}
		containers[projectName] = append(names, name)
	}

	return containers, nil
}

func instancesShutdown(s *state.State) error {
	var wg sync.WaitGroup

	dbAvailable := true

	// Get all the instances
	instances, err := instance.LoadNodeAll(s, instancetype.Any)
	if err != nil {
		// Mark database as offline
		dbAvailable = false
		instances = []instance.Instance{}

		// List all containers on disk
		cnames, err := containersOnDisk()
		if err != nil {
			return err
		}

		for project, names := range cnames {
			for _, name := range names {
				inst, err := instance.Load(s, db.InstanceArgs{
					Project: project,
					Name:    name,
					Config:  make(map[string]string),
				}, nil)
				if err != nil {
					return err
				}

				instances = append(instances, inst)
			}
		}
	}

	sort.Sort(containerStopList(instances))

	if dbAvailable {
		// Reset all instances states
		err = s.Cluster.ResetInstancesPowerState()
		if err != nil {
			return err
		}
	}

	var lastPriority int

	if len(instances) != 0 {
		lastPriority, _ = strconv.Atoi(instances[0].ExpandedConfig()["boot.stop.priority"])
	}

	for _, c := range instances {
		priority, _ := strconv.Atoi(c.ExpandedConfig()["boot.stop.priority"])

		// Enforce shutdown priority
		if priority != lastPriority {
			lastPriority = priority

			// Wait for instances with higher priority to finish
			wg.Wait()
		}

		// Record the current state
		lastState := c.State()

		// Stop the container
		if lastState != "BROKEN" && lastState != "STOPPED" {
			// Determinate how long to wait for the instance to shutdown cleanly
			var timeoutSeconds int
			value, ok := c.ExpandedConfig()["boot.host_shutdown_timeout"]
			if ok {
				timeoutSeconds, _ = strconv.Atoi(value)
			} else {
				timeoutSeconds = 30
			}

			// Stop the instance
			wg.Add(1)
			go func(c instance.Instance, lastState string) {
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
