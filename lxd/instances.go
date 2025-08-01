package main

import (
	"context"
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
	"github.com/canonical/lxd/lxd/operations"
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
	Post: APIEndpointAction{Handler: instancesPost, AccessHandler: allowPermission(entity.TypeProject, auth.EntitlementCanCreateInstances), ContentTypes: []string{"application/json", "application/octet-stream"}},
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
	Post:   APIEndpointAction{Handler: instanceFileHandler, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanAccessFiles, "name"), ContentTypes: []string{"application/octet-stream"}},
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
	Post:   APIEndpointAction{Handler: instanceMetadataTemplatesPost, AccessHandler: allowPermission(entity.TypeInstance, auth.EntitlementCanEdit, "name"), ContentTypes: []string{"application/octet-stream"}},
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
						return tx.UpsertWarningLocalNode(ctx, inst.Project().Name, entity.TypeInstance, inst.ID(), warningtype.InstanceAutostartFailure, err.Error())
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
					Node:    s.ServerName, // Set Node field to local node.
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

// isInstanceBusy checks if the instance is currently busy: if it has an associated operation that is in a running state.
func isInstanceBusy(inst instance.Instance, instancesToOps map[string]*operations.Operation, instancesToOpsMu *sync.Mutex) bool {
	if instancesToOps == nil {
		return false
	}

	if len(instancesToOps) == 0 {
		return false
	}

	instanceURL := entity.InstanceURL(inst.Project().Name, inst.Name()).String()
	instancesToOpsMu.Lock()
	defer instancesToOpsMu.Unlock()

	op, ok := instancesToOps[instanceURL]
	return ok && op != nil && op.Status() == api.Running && op.Class() != operations.OperationClassToken
}

// instancesShutdown orchestrates a controlled, priority-based shutdown of multiple instances while handling
// concurrent operations, timeouts, and potential cancellation.
//
// Algorithm overview:
// 1. Instances are sorted by their `boot.stop.priority`.
// 2. Shutdown concurrency is limited to min(number of instances, CPU cores).
// 3. Instances are processed in batches of the same priority.
// 4. Each batch completes before starting the next lower priority batch.
// 5. Worker goroutines handle instance shutdown operations. This pool is fed through the `instShutdownCh` channel.
// 6. Busy instances (with running operations) are tracked in a separate goroutine which is fed through the `busyInstCh“ channel and resend for shutdown (sent to `instShutdownCh`) once operation completes.
// 7. Context cancellation send the remaining instances to the workers to be shutdown.
//
// Examples:
//
// 1. Normal priority-based shutdown (boot.stop.priority values: 2, 1, 0)
//   - All priority 2 instances shut down concurrently
//   - After all priority 2 instances complete, priority 1 instances start
//   - After all priority 1 instances complete, priority 0 instances start
//
// 2. Busy instance handling:
//   - Instance has running operation (e.g., backup, snapshot, etc.)
//   - Instance is sent to busyInstancesCh and tracked by the busy instance tracker goroutine
//   - Tracker periodically checks if operation completed
//   - Once no longer busy, instance is sent back to instShutdownCh for shutdown
//
// 3. Context cancellation (e.g., during daemon shutdown timeout):
//   - Main context cancelled
//   - Cancellation of context is handled in the tracking goroutine and send remaining instances to instShutdownCh for shutdown.
//
// 4. Custom timeout handling:
//   - Each instance can specify boot.host_shutdown_timeout
//   - Instances get graceful shutdown with their specified timeout
//   - If graceful shutdown fails, fallback to force stop
//
// 5. Power state tracking:
//   - Each instance shutdown preserves the last power state as "RUNNING"
//   - Ensures instances restart when LXD daemon comes back up
func instancesShutdown(ctx context.Context, instances []instance.Instance) {
	// List all pending operations tied to instances.
	instancesToOpsMu := sync.Mutex{}
	instancesToOps, err := pendingInstanceOperations()
	if err != nil {
		logger.Error("Failed to get entity to pending operations map", logger.Ctx{"err": err})
	}

	sort.Sort(instanceStopList(instances))

	// Limit shutdown concurrency to number of instances or number of CPU cores (which ever is less).
	var wg sync.WaitGroup
	instShutdownCh := make(chan instance.Instance)
	maxConcurrent := runtime.NumCPU()
	instCount := len(instances)
	if instCount < maxConcurrent {
		maxConcurrent = instCount
	}

	// Start the busy instance tracker if instancesToOps is provided.
	busyInstancesCh := make(chan instance.Instance)
	if len(instancesToOps) > 0 {
		go func() {
			// Map to track busy instances
			busyInstances := make(map[string]instance.Instance)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()

			var lastWaitingLog time.Time

			for {
				select {
				case inst, ok := <-busyInstancesCh:
					if !ok {
						logger.Debug("Finishing busy instances tracking")
						return
					}

					logger.Debug("Instance received for busy tracking", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
					instanceURL := entity.InstanceURL(inst.Project().Name, inst.Name()).String()
					busyInstances[instanceURL] = inst
				case <-ticker.C:
					ctxErr := ctx.Err()
					if ctxErr != nil {
						logger.Info("Skipping waiting for instance operations to finish")
					} else if time.Since(lastWaitingLog) > (time.Second * 10) {
						logger.Info("Waiting for instance operations to finish", logger.Ctx{"instances": len(busyInstances)})
						lastWaitingLog = time.Now()
					}

					for instanceURL, inst := range busyInstances {
						if ctxErr != nil || !isInstanceBusy(inst, instancesToOps, &instancesToOpsMu) {
							delete(busyInstances, instanceURL)
							logger.Debug("Instance removed from busy tracking, sending to shutdown channel", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
							instShutdownCh <- inst
						}
					}
				}
			}
		}()
	}

	for range maxConcurrent {
		go func(instShutdownCh <-chan instance.Instance) {
			for inst := range instShutdownCh {
				l := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})

				l.Debug("Instance received for shutdown")
				// Determine how long to wait for the instance to shutdown cleanly.
				timeoutSeconds := 30
				value, ok := inst.ExpandedConfig()["boot.host_shutdown_timeout"]
				if ok {
					timeoutSeconds, _ = strconv.Atoi(value)
				}

				err := inst.Shutdown(time.Second * time.Duration(timeoutSeconds))
				if err != nil {
					l.Warn("Failed shutting down instance, forcefully stopping", logger.Ctx{"err": err})
					err = inst.Stop(false)
					if err != nil {
						l.Warn("Failed forcefully stopping instance", logger.Ctx{"err": err})
					}
				}

				if inst.ID() > 0 {
					// If DB was available then the instance shutdown process will have set
					// the last power state to STOPPED, so set that back to RUNNING so that
					// when LXD restarts the instance will be started again.
					err = inst.VolatileSet(map[string]string{"volatile.last_state.power": instance.PowerStateRunning})
					if err != nil {
						l.Warn("Failed updating volatile.last_state.power", logger.Ctx{"err": err})
					}
				}

				wg.Done()
				l.Debug("Instance shutdown complete")
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
			logger.Debug("Waiting for instances to be shutdown", logger.Ctx{"stopPriority": currentBatchPriority})
			wg.Wait()
			logger.Info("Stopping instances", logger.Ctx{"stopPriority": currentBatchPriority})
		}

		wg.Add(1)
		if ctx.Err() == nil && isInstanceBusy(inst, instancesToOps, &instancesToOpsMu) {
			busyInstancesCh <- inst
		} else {
			instShutdownCh <- inst
		}
	}

	wg.Wait()
	close(instShutdownCh)
	close(busyInstancesCh)
}
