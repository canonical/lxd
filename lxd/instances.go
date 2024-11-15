package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/warningtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/lxd/warnings"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
)

var instancesCmd = APIEndpoint{
	Name:        "instances",
	Path:        "instances",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "containers", Path: "containers"},
		{Name: "vms", Path: "virtual-machines"},
	},

	Get:  APIEndpointAction{Handler: instancesGet, AccessHandler: allowProjectResourceList},
	Post: APIEndpointAction{Handler: instancesPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanCreateInstances)},
	Put:  APIEndpointAction{Handler: instancesPut, AccessHandler: allowProjectResourceList},
}

var instanceCmd = APIEndpoint{
	Name:        "instance",
	Path:        "instances/{name}",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "container", Path: "containers/{name}"},
		{Name: "vm", Path: "virtual-machines/{name}"},
	},

	Get:    APIEndpointAction{Handler: instanceGet, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanView, "name")},
	Put:    APIEndpointAction{Handler: instancePut, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanEdit, "name")},
	Delete: APIEndpointAction{Handler: instanceDelete, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanDelete, "name")},
	Post:   APIEndpointAction{Handler: instancePost, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanEdit, "name")},
	Patch:  APIEndpointAction{Handler: instancePatch, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanEdit, "name")},
}

var instanceUEFIVarsCmd = APIEndpoint{
	Name:        "instanceUEFIVars",
	Path:        "instances/{name}/uefi-vars",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "vmUEFIVars", Path: "virtual-machines/{name}/uefi-vars"},
	},

	Get: APIEndpointAction{Handler: instanceUEFIVarsGet, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanView, "name")},
	Put: APIEndpointAction{Handler: instanceUEFIVarsPut, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanEdit, "name")},
}

var instanceRebuildCmd = APIEndpoint{
	Name:        "instanceRebuild",
	Path:        "instances/{name}/rebuild",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "containerRebuild", Path: "containers/{name}/rebuild"},
		{Name: "vmRebuild", Path: "virtual-machines/{name}/rebuild"},
	},

	Post: APIEndpointAction{Handler: instanceRebuildPost, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanEdit, "name")},
}

var instanceStateCmd = APIEndpoint{
	Name:        "instanceState",
	Path:        "instances/{name}/state",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "containerState", Path: "containers/{name}/state"},
		{Name: "vmState", Path: "virtual-machines/{name}/state"},
	},

	Get: APIEndpointAction{Handler: instanceState, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanView, "name")},
	Put: APIEndpointAction{Handler: instanceStatePut, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanUpdateState, "name")},
}

var instanceSFTPCmd = APIEndpoint{
	Name:        "instanceFile",
	Path:        "instances/{name}/sftp",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "containerFile", Path: "containers/{name}/sftp"},
		{Name: "vmFile", Path: "virtual-machines/{name}/sftp"},
	},

	Get: APIEndpointAction{Handler: instanceSFTPHandler, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanConnectSFTP, "name")},
}

var instanceFileCmd = APIEndpoint{
	Name:        "instanceFile",
	Path:        "instances/{name}/files",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "containerFile", Path: "containers/{name}/files"},
		{Name: "vmFile", Path: "virtual-machines/{name}/files"},
	},

	Get:    APIEndpointAction{Handler: instanceFileHandler, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanAccessFiles, "name")},
	Head:   APIEndpointAction{Handler: instanceFileHandler, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanAccessFiles, "name")},
	Post:   APIEndpointAction{Handler: instanceFileHandler, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanAccessFiles, "name")},
	Delete: APIEndpointAction{Handler: instanceFileHandler, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanAccessFiles, "name")},
}

var instanceSnapshotsCmd = APIEndpoint{
	Name:        "instanceSnapshots",
	Path:        "instances/{name}/snapshots",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "containerSnapshots", Path: "containers/{name}/snapshots"},
		{Name: "vmSnapshots", Path: "virtual-machines/{name}/snapshots"},
	},

	Get:  APIEndpointAction{Handler: instanceSnapshotsGet, AccessHandler: allowProjectResourceList},
	Post: APIEndpointAction{Handler: instanceSnapshotsPost, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanManageSnapshots, "name")},
}

var instanceSnapshotCmd = APIEndpoint{
	Name:        "instanceSnapshot",
	Path:        "instances/{name}/snapshots/{snapshotName}",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "containerSnapshot", Path: "containers/{name}/snapshots/{snapshotName}"},
		{Name: "vmSnapshot", Path: "virtual-machines/{name}/snapshots/{snapshotName}"},
	},

	Get:    APIEndpointAction{Handler: instanceSnapshotHandler, AccessHandler: allowPermission(entity.TypeInstanceSnapshot, auth.EntitlementCanView, "name", "snapshotName")},
	Post:   APIEndpointAction{Handler: instanceSnapshotHandler, AccessHandler: allowPermission(entity.TypeInstanceSnapshot, auth.EntitlementCanEdit, "name", "snapshotName")},
	Delete: APIEndpointAction{Handler: instanceSnapshotHandler, AccessHandler: allowPermission(entity.TypeInstanceSnapshot, auth.EntitlementCanDelete, "name", "snapshotName")},
	Patch:  APIEndpointAction{Handler: instanceSnapshotHandler, AccessHandler: allowPermission(entity.TypeInstanceSnapshot, auth.EntitlementCanEdit, "name", "snapshotName")},
	Put:    APIEndpointAction{Handler: instanceSnapshotHandler, AccessHandler: allowPermission(entity.TypeInstanceSnapshot, auth.EntitlementCanEdit, "name", "snapshotName")},
}

var instanceConsoleCmd = APIEndpoint{
	Name:        "instanceConsole",
	Path:        "instances/{name}/console",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "containerConsole", Path: "containers/{name}/console"},
		{Name: "vmConsole", Path: "virtual-machines/{name}/console"},
	},

	Get:    APIEndpointAction{Handler: instanceConsoleLogGet, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanView, "name")},
	Post:   APIEndpointAction{Handler: instanceConsolePost, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanAccessConsole, "name")},
	Delete: APIEndpointAction{Handler: instanceConsoleLogDelete, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanEdit, "name")},
}

var instanceExecCmd = APIEndpoint{
	Name:        "instanceExec",
	Path:        "instances/{name}/exec",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "containerExec", Path: "containers/{name}/exec"},
		{Name: "vmExec", Path: "virtual-machines/{name}/exec"},
	},

	Post: APIEndpointAction{Handler: instanceExecPost, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanExec, "name")},
}

var instanceMetadataCmd = APIEndpoint{
	Name:        "instanceMetadata",
	Path:        "instances/{name}/metadata",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "containerMetadata", Path: "containers/{name}/metadata"},
		{Name: "vmMetadata", Path: "virtual-machines/{name}/metadata"},
	},

	Get:   APIEndpointAction{Handler: instanceMetadataGet, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanView, "name")},
	Patch: APIEndpointAction{Handler: instanceMetadataPatch, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanEdit, "name")},
	Put:   APIEndpointAction{Handler: instanceMetadataPut, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanEdit, "name")},
}

var instanceMetadataTemplatesCmd = APIEndpoint{
	Name:        "instanceMetadataTemplates",
	Path:        "instances/{name}/metadata/templates",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "containerMetadataTemplates", Path: "containers/{name}/metadata/templates"},
		{Name: "vmMetadataTemplates", Path: "virtual-machines/{name}/metadata/templates"},
	},

	Get:    APIEndpointAction{Handler: instanceMetadataTemplatesGet, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanView, "name")},
	Post:   APIEndpointAction{Handler: instanceMetadataTemplatesPost, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanEdit, "name")},
	Delete: APIEndpointAction{Handler: instanceMetadataTemplatesDelete, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanEdit, "name")},
}

var instanceBackupsCmd = APIEndpoint{
	Name:        "instanceBackups",
	Path:        "instances/{name}/backups",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "containerBackups", Path: "containers/{name}/backups"},
		{Name: "vmBackups", Path: "virtual-machines/{name}/backups"},
	},

	Get:  APIEndpointAction{Handler: instanceBackupsGet, AccessHandler: allowProjectResourceList},
	Post: APIEndpointAction{Handler: instanceBackupsPost, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanManageBackups, "name")},
}

var instanceBackupCmd = APIEndpoint{
	Name:        "instanceBackup",
	Path:        "instances/{name}/backups/{backupName}",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "containerBackup", Path: "containers/{name}/backups/{backupName}"},
		{Name: "vmBackup", Path: "virtual-machines/{name}/backups/{backupName}"},
	},

	Get:    APIEndpointAction{Handler: instanceBackupGet, AccessHandler: allowPermission(entity.TypeInstanceBackup, auth.EntitlementCanView, "name", "backupName")},
	Post:   APIEndpointAction{Handler: instanceBackupPost, AccessHandler: allowPermission(entity.TypeInstanceBackup, auth.EntitlementCanEdit, "name", "backupName")},
	Delete: APIEndpointAction{Handler: instanceBackupDelete, AccessHandler: allowPermission(entity.TypeInstanceBackup, auth.EntitlementCanEdit, "name", "backupName")},
}

var instanceBackupExportCmd = APIEndpoint{
	Name:        "instanceBackupExport",
	Path:        "instances/{name}/backups/{backupName}/export",
	MetricsType: entity.TypeInstance,
	Aliases: []APIEndpointAlias{
		{Name: "containerBackupExport", Path: "containers/{name}/backups/{backupName}/export"},
		{Name: "vmBackupExport", Path: "virtual-machines/{name}/backups/{backupName}/export"},
	},

	Get: APIEndpointAction{Handler: instanceBackupExportGet, AccessHandler: allowPermission(entity.TypeInstanceBackup, auth.EntitlementCanView, "name", "backupName")},
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

var instancesStartMu sync.Mutex

// instanceShouldAutoStart returns whether the instance should be auto-started.
// Returns true if the conditions below are all met:
// 1. security.protection.start is not enabled or not set.
// 2. boot.autostart is enabled or boot.autostart is not set and instance was previously running.
func instanceShouldAutoStart(inst instance.Instance) bool {
	config := inst.ExpandedConfig()
	autoStart := config["boot.autostart"]
	lastState := config["volatile.last_state.power"]
	protectStart := config["security.protection.start"]

	return shared.IsFalseOrEmpty(protectStart) && (shared.IsTrue(autoStart) || (autoStart == "" && lastState == instance.PowerStateRunning))
}

func instancesStart(s *state.State, instances []instance.Instance) {
	// Check if the cluster is currently evacuated.
	if s.DB.Cluster.LocalNodeIsEvacuated() {
		return
	}

	// Acquire startup lock.
	instancesStartMu.Lock()
	defer instancesStartMu.Unlock()

	// Sort based on instance boot priority.
	sort.Sort(instanceAutostartList(instances))

	// Let's make up to 3 attempts to start instances.
	maxAttempts := 3

	// Start the instances
	for _, inst := range instances {
		if !instanceShouldAutoStart(inst) {
			continue
		}

		// If already running, we're done.
		if inst.IsRunning() {
			continue
		}

		// Get the instance config.
		config := inst.ExpandedConfig()
		autoStartDelay := config["boot.autostart.delay"]

		instLogger := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})

		// Try to start the instance.
		var attempt = 0
		for {
			attempt++
			err := inst.Start(false)
			if err != nil {
				if api.StatusErrorCheck(err, http.StatusServiceUnavailable) {
					break // Don't log or retry instances that are not ready to start yet.
				}

				instLogger.Warn("Failed auto start instance attempt", logger.Ctx{"attempt": attempt, "maxAttempts": maxAttempts, "err": err})

				if attempt >= maxAttempts {
					warnErr := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
						// If unable to start after 3 tries, record a warning.
						return tx.UpsertWarningLocalNode(ctx, inst.Project().Name, entity.TypeInstance, inst.ID(), warningtype.InstanceAutostartFailure, fmt.Sprintf("%v", err))
					})
					if warnErr != nil {
						instLogger.Warn("Failed to create instance autostart failure warning", logger.Ctx{"err": warnErr})
					}

					instLogger.Error("Failed to auto start instance", logger.Ctx{"err": err})

					break
				}

				time.Sleep(5 * time.Second)

				continue
			}

			// Resolve any previous warning.
			warnErr := warnings.ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(s.DB.Cluster, inst.Project().Name, warningtype.InstanceAutostartFailure, entity.TypeInstance, inst.ID())
			if warnErr != nil {
				instLogger.Warn("Failed to resolve instance autostart failure warning", logger.Ctx{"err": warnErr})
			}

			// Wait the auto-start delay if set.
			autoStartDelayInt, err := strconv.Atoi(autoStartDelay)
			if err == nil {
				time.Sleep(time.Duration(autoStartDelayInt) * time.Second)
			}

			break
		}
	}
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

// Return all local instances on disk (if instance is running, it will attempt to populate the instance's local
// and expanded config using the backup.yaml file). It will clear the instance's profiles property to avoid needing
// to enrich them from the database.
func instancesOnDisk(s *state.State) ([]instance.Instance, error) {
	var err error

	instancePaths := map[instancetype.Type]string{
		instancetype.Container: shared.VarPath("containers"),
		instancetype.VM:        shared.VarPath("virtual-machines"),
	}

	instanceTypeNames := make(map[instancetype.Type][]os.DirEntry, 2)

	instanceTypeNames[instancetype.Container], err = os.ReadDir(instancePaths[instancetype.Container])
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	instanceTypeNames[instancetype.VM], err = os.ReadDir(instancePaths[instancetype.VM])
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	instances := make([]instance.Instance, 0, len(instanceTypeNames[instancetype.Container])+len(instanceTypeNames[instancetype.VM]))
	for instanceType, instanceNames := range instanceTypeNames {
		for _, file := range instanceNames {
			// Convert file name to project name and instance name.
			projectName, instanceName := project.InstanceParts(file.Name())

			var inst instance.Instance

			// Try and parse the backup file (if instance is running).
			// This allows us to stop VMs which require access to the vsock ID and volatile UUID.
			// Also generally it ensures that all devices are stopped cleanly too.
			backupYamlPath := filepath.Join(instancePaths[instanceType], file.Name(), "backup.yaml")
			if shared.PathExists(backupYamlPath) {
				inst, err = instance.LoadFromBackup(s, projectName, filepath.Join(instancePaths[instanceType], file.Name()))
				if err != nil {
					logger.Warn("Failed loading instance", logger.Ctx{"project": projectName, "instance": instanceName, "backup_file": backupYamlPath, "err": err})
				}
			}

			if inst == nil {
				// Initialise dbArgs with a very basic config.
				// This will not be sufficient to stop an instance cleanly.
				instDBArgs := &db.InstanceArgs{
					Type:    instanceType,
					Project: projectName,
					Name:    instanceName,
					Config:  make(map[string]string),
				}

				emptyProject := api.Project{
					Name: projectName,
				}

				inst, err = instance.Load(s, *instDBArgs, emptyProject)
				if err != nil {
					logger.Warn("Failed loading instance", logger.Ctx{"project": projectName, "instance": instanceName, "err": err})
					continue
				}
			}

			instances = append(instances, inst)
		}
	}

	return instances, nil
}

func instancesShutdown(instances []instance.Instance) {
	sort.Sort(instanceStopList(instances))

	// Limit shutdown concurrency to number of instances or number of CPU cores (which ever is less).
	var wg sync.WaitGroup
	instShutdownCh := make(chan instance.Instance)
	maxConcurrent := runtime.NumCPU()
	instCount := len(instances)
	if instCount < maxConcurrent {
		maxConcurrent = instCount
	}

	for i := 0; i < maxConcurrent; i++ {
		go func(instShutdownCh <-chan instance.Instance) {
			for inst := range instShutdownCh {
				// Determine how long to wait for the instance to shutdown cleanly.
				timeoutSeconds := 30
				value, ok := inst.ExpandedConfig()["boot.host_shutdown_timeout"]
				if ok {
					timeoutSeconds, _ = strconv.Atoi(value)
				}

				err := inst.Shutdown(time.Second * time.Duration(timeoutSeconds))
				if err != nil {
					logger.Warn("Failed shutting down instance, forcefully stopping", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "err": err})
					err = inst.Stop(false)
					if err != nil {
						logger.Warn("Failed forcefully stopping instance", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "err": err})
					}
				}

				if inst.ID() > 0 {
					// If DB was available then the instance shutdown process will have set
					// the last power state to STOPPED, so set that back to RUNNING so that
					// when LXD restarts the instance will be started again.
					_ = inst.VolatileSet(map[string]string{"volatile.last_state.power": instance.PowerStateRunning})
				}

				wg.Done()
			}
		}(instShutdownCh)
	}

	var currentBatchPriority int
	for i, inst := range instances {
		// Skip stopped instances.
		if !inst.IsRunning() {
			continue
		}

		priority, _ := strconv.Atoi(inst.ExpandedConfig()["boot.stop.priority"])

		// Shutdown instances in priority batches, logging at the start of each batch.
		if i == 0 || priority != currentBatchPriority {
			currentBatchPriority = priority

			// Wait for instances with higher priority to finish before starting next batch.
			wg.Wait()
			logger.Info("Stopping instances", logger.Ctx{"stopPriority": currentBatchPriority})
		}

		wg.Add(1)
		instShutdownCh <- inst
	}

	wg.Wait()
	close(instShutdownCh)
}
