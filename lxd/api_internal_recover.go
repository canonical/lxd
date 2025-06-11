package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/backup"
	backupConfig "github.com/canonical/lxd/lxd/backup/config"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	deviceConfig "github.com/canonical/lxd/lxd/device/config"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/project"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	storageDrivers "github.com/canonical/lxd/lxd/storage/drivers"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/osarch"
	"github.com/canonical/lxd/shared/revert"
)

// Define API endpoints for recover actions.
var internalRecoverValidateCmd = APIEndpoint{
	Path: "recover/validate",

	Post: APIEndpointAction{Handler: internalRecoverValidate, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

var internalRecoverImportCmd = APIEndpoint{
	Path: "recover/import",

	Post: APIEndpointAction{Handler: internalRecoverImport, AccessHandler: allowPermission(entity.TypeServer, auth.EntitlementCanEdit)},
}

// init recover adds API endpoints to handler slice.
func init() {
	apiInternal = append(apiInternal, internalRecoverValidateCmd, internalRecoverImportCmd)
}

// internalRecoverValidatePost is used to initiate a recovery validation scan.
type internalRecoverValidatePost struct {
	Pools []api.StoragePoolsPost `json:"pools" yaml:"pools"`
}

// internalRecoverValidateVolume provides info about a missing volume that the recovery validation scan found.
type internalRecoverValidateVolume struct {
	Name          string `json:"name"          yaml:"name"`          // Name of volume.
	Type          string `json:"type"          yaml:"type"`          // Same as Type from StorageVolumesPost (container, custom or virtual-machine).
	SnapshotCount int    `json:"snapshotCount" yaml:"snapshotCount"` // Count of snapshots found for volume.
	Project       string `json:"project"       yaml:"project"`       // Project the volume belongs to.
	Pool          string `json:"pool"          yaml:"pool"`          // Pool the volume belongs to.
}

// internalRecoverValidateResult returns the result of the validation scan.
type internalRecoverValidateResult struct {
	UnknownVolumes   []internalRecoverValidateVolume // Volumes that could be imported.
	DependencyErrors []string                        // Errors that are preventing import from proceeding.
}

// internalRecoverImportPost is used to initiate a recovert import.
type internalRecoverImportPost struct {
	Pools []api.StoragePoolsPost `json:"pools" yaml:"pools"`
}

// internalRecoverScan provides the discovery and import functionality for both recovery validate and import steps.
func internalRecoverScan(ctx context.Context, s *state.State, userPools []api.StoragePoolsPost, validateOnly bool) response.Response {
	var err error
	var projects map[string]*api.Project
	var projectProfiles map[string][]*api.Profile
	var projectNetworks map[string]map[int64]api.Network

	// Retrieve all project, profile and network info in a single transaction so we can use it for all
	// imported instances and volumes, and avoid repeatedly querying the same information.
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Load list of projects for validation.
		ps, err := dbCluster.GetProjects(ctx, tx.Tx())
		if err != nil {
			return err
		}

		// Convert to map for lookups by name later.
		projects = make(map[string]*api.Project, len(ps))
		for i := range ps {
			project, err := ps[i].ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			projects[ps[i].Name] = project
		}

		// Load list of project/profile names for validation.
		profiles, err := dbCluster.GetProfiles(ctx, tx.Tx())
		if err != nil {
			return err
		}

		profileConfigs, err := dbCluster.GetConfig(ctx, tx.Tx(), "profile")
		if err != nil {
			return err
		}

		profileDevices, err := dbCluster.GetDevices(ctx, tx.Tx(), "profile")
		if err != nil {
			return err
		}

		// Convert to map for lookups by project name later.
		projectProfiles = make(map[string][]*api.Profile)
		for _, profile := range profiles {
			if projectProfiles[profile.Project] == nil {
				projectProfiles[profile.Project] = []*api.Profile{}
			}

			apiProfile, err := profile.ToAPI(ctx, tx.Tx(), profileConfigs, profileDevices)
			if err != nil {
				return err
			}

			projectProfiles[profile.Project] = append(projectProfiles[profile.Project], apiProfile)
		}

		// Load list of project/network names for validation.
		projectNetworks, err = tx.GetCreatedNetworks(ctx)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed getting validate dependency check info: %w", err))
	}

	res := internalRecoverValidateResult{}

	revert := revert.New()
	defer revert.Fail()

	// addDependencyError adds an error to the list of dependency errors if not already present in list.
	addDependencyError := func(err error) {
		errStr := err.Error()

		if !slices.Contains(res.DependencyErrors, errStr) {
			res.DependencyErrors = append(res.DependencyErrors, errStr)
		}
	}

	// Used to store the unknown volumes for each pool & project.
	poolsProjectVols := make(map[string]map[string][]*backupConfig.Config)

	// Used to store a handle to each pool containing user supplied config.
	pools := make(map[string]storagePools.Pool)

	// Iterate the pools finding unknown volumes and perform validation.
	for _, p := range userPools {
		pool, err := storagePools.LoadByName(s, p.Name)
		if err != nil {
			if !response.IsNotFoundError(err) {
				return response.SmartError(fmt.Errorf("Failed loading existing pool %q: %w", p.Name, err))
			}

			// If the pool DB record doesn't exist, and we are clustered, then don't proceed
			// any further as we do not support pool DB record recovery when clustered.
			if s.ServerClustered {
				return response.BadRequest(errors.New("Storage pool recovery not supported when clustered"))
			}

			// If pool doesn't exist in DB, initialise a temporary pool with the supplied info.
			poolInfo := api.StoragePool{
				Name:   p.Name,
				Driver: p.Driver,
				Status: api.StoragePoolStatusCreated,
			}

			poolInfo.SetWritable(p.StoragePoolPut)

			pool, err = storagePools.NewTemporary(s, &poolInfo)
			if err != nil {
				return response.SmartError(fmt.Errorf("Failed to initialise unknown pool %q: %w", p.Name, err))
			}

			// Populate configuration with default values.
			err := pool.Driver().FillConfig()
			if err != nil {
				return response.SmartError(fmt.Errorf("Failed to evaluate the default configuration values for unknown pool %q: %w", p.Name, err))
			}

			err = pool.Driver().Validate(poolInfo.Config)
			if err != nil {
				return response.SmartError(fmt.Errorf("Failed config validation for unknown pool %q: %w", p.Name, err))
			}
		}

		// Record this pool to be used during import stage, assuming validation passes.
		pools[p.Name] = pool

		// Try to mount the pool.
		ourMount, err := pool.Mount()
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed mounting pool %q: %w", pool.Name(), err))
		}

		// Unmount pool when done if not existing in DB after function has finished.
		// This way if we are dealing with an existing pool or have successfully created the DB record then
		// we won't unmount it. As we should leave successfully imported pools mounted.
		if ourMount {
			defer func() {
				cleanupPool := pools[pool.Name()]
				if cleanupPool != nil && cleanupPool.ID() == storagePools.PoolIDTemporary {
					_, _ = cleanupPool.Unmount()
				}
			}()

			revert.Add(func() {
				cleanupPool := pools[pool.Name()]
				_, _ = cleanupPool.Unmount() // Defer won't do it if record exists, so unmount on failure.
			})
		}

		// Get list of unknown volumes on pool.
		poolProjectVols, err := pool.ListUnknownVolumes(nil)
		if err != nil {
			if errors.Is(err, storageDrivers.ErrNotSupported) {
				continue // Ignore unsupported storage drivers.
			}

			return response.SmartError(fmt.Errorf("Failed checking volumes on pool %q: %w", pool.Name(), err))
		}

		// Store for consumption after validation scan to avoid needing to reprocess.
		poolsProjectVols[p.Name] = poolProjectVols

		// Check dependencies are met for each volume.
		for projectName, poolVols := range poolProjectVols {
			// Check project exists in database.
			projectInfo := projects[projectName]

			// Look up effective project names for profiles and networks.
			var profileProjectname string
			var networkProjectName string

			if projectInfo == nil {
				addDependencyError(fmt.Errorf("Project %q", projectName))
				continue // Skip further validation if project is missing.
			}

			profileProjectname = project.ProfileProjectFromRecord(projectInfo)
			networkProjectName = project.NetworkProjectFromRecord(projectInfo)

			for _, poolVol := range poolVols {
				if poolVol.Instance == nil {
					continue // Skip dependency checks for non-instance volumes.
				}

				// Check that the instance's profile dependencies are met.
				for _, poolInstProfileName := range poolVol.Instance.Profiles {
					foundProfile := false
					for _, profile := range projectProfiles[profileProjectname] {
						if profile.Name == poolInstProfileName {
							foundProfile = true
						}
					}

					if !foundProfile {
						addDependencyError(fmt.Errorf("Profile %q in project %q", poolInstProfileName, projectName))
					}
				}

				// Check that the instance's NIC network dependencies are met.
				for _, devConfig := range poolVol.Instance.ExpandedDevices {
					if devConfig["type"] != "nic" {
						continue
					}

					if devConfig["network"] == "" {
						continue
					}

					foundNetwork := false
					for _, n := range projectNetworks[networkProjectName] {
						if n.Name == devConfig["network"] {
							foundNetwork = true
							break
						}
					}

					if !foundNetwork {
						addDependencyError(fmt.Errorf("Network %q in project %q", devConfig["network"], projectName))
					}
				}
			}
		}
	}

	// If in validation mode or if there are dependency errors, return discovered unknown volumes, along with
	// any dependency errors.
	if validateOnly || len(res.DependencyErrors) > 0 {
		for poolName, poolProjectVols := range poolsProjectVols {
			for projectName, poolVols := range poolProjectVols {
				for _, poolVol := range poolVols {
					var displayType, displayName string
					var displaySnapshotCount int

					// Build display fields for scan results.
					if poolVol.Instance != nil {
						displayType = poolVol.Instance.Type
						displayName = poolVol.Instance.Name
						displaySnapshotCount = len(poolVol.Snapshots)
					} else if poolVol.Bucket != nil {
						displayType = "bucket"
						displayName = poolVol.Bucket.Name
						displaySnapshotCount = 0
					} else {
						displayType = "volume"

						customVol, err := poolVol.CustomVolume()
						if err != nil {
							return response.SmartError(fmt.Errorf("Failed getting the custom volume: %w", err))
						}

						displayName = customVol.Name
						displaySnapshotCount = len(customVol.Snapshots)
					}

					res.UnknownVolumes = append(res.UnknownVolumes, internalRecoverValidateVolume{
						Pool:          poolName,
						Project:       projectName,
						Type:          displayType,
						Name:          displayName,
						SnapshotCount: displaySnapshotCount,
					})
				}
			}
		}

		return response.SyncResponse(true, &res)
	}

	// If in import mode and no dependency errors, then re-create missing DB records.

	for _, pool := range pools {
		// Create missing storage pool DB record if neeed.
		if pool.ID() == storagePools.PoolIDTemporary {
			var instPoolVol *backupConfig.Config // Instance volume used for new pool record.
			var poolID int64                     // Pool ID of created pool record.

			var poolVols []*backupConfig.Config
			for _, value := range poolsProjectVols[pool.Name()] {
				poolVols = append(poolVols, value...)
			}

			var rootVolPool *api.StoragePool

			// Search unknown volumes looking for an instance volume that can be used to
			// restore the pool DB config from. This is preferable over using the user
			// supplied config as it will include any additional settings not supplied.
			for _, poolVol := range poolVols {
				rootVolPool, err = poolVol.RootVolumePool()
				if err != nil {
					// We are looking for a volume with a pool configuration.
					// Skip this volume if it doesn't contain what we are looking for.
					continue
				}

				if rootVolPool.Config != nil {
					instPoolVol = poolVol
					break // Stop search once we've found an instance with pool config.
				}
			}

			if instPoolVol != nil {
				// Create storage pool DB record from config in the instance.
				logger.Info("Creating storage pool DB record from instance config", logger.Ctx{"name": rootVolPool.Name, "description": rootVolPool.Description, "driver": rootVolPool.Driver, "config": rootVolPool.Config})
				poolID, err = dbStoragePoolCreateAndUpdateCache(ctx, s, rootVolPool.Name, rootVolPool.Description, rootVolPool.Driver, rootVolPool.Config)
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed creating storage pool %q database entry: %w", pool.Name(), err))
				}
			} else {
				// Create storage pool DB record from config supplied by user if not
				// instance volume pool config found.
				poolDriverName := pool.Driver().Info().Name
				poolDriverConfig := pool.Driver().Config()
				logger.Info("Creating storage pool DB record from user config", logger.Ctx{"name": pool.Name(), "driver": poolDriverName, "config": poolDriverConfig})
				poolID, err = dbStoragePoolCreateAndUpdateCache(ctx, s, pool.Name(), "", poolDriverName, poolDriverConfig)
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed creating storage pool %q database entry: %w", pool.Name(), err))
				}
			}

			revert.Add(func() {
				_ = dbStoragePoolDeleteAndUpdateCache(context.Background(), s, pool.Name())
			})

			// Set storage pool node to storagePoolCreated.
			// Must come before storage pool is loaded from the database.
			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.StoragePoolNodeCreated(poolID)
			})
			if err != nil {
				return response.SmartError(fmt.Errorf("Failed marking storage pool %q local status as created: %w", pool.Name(), err))
			}

			logger.Debug("Marked storage pool local status as created", logger.Ctx{"pool": pool.Name()})

			newPool, err := storagePools.LoadByName(s, pool.Name())
			if err != nil {
				return response.SmartError(fmt.Errorf("Failed loading created storage pool %q: %w", pool.Name(), err))
			}

			// Record this newly created pool so that defer doesn't unmount on return.
			pools[pool.Name()] = newPool
			pool = newPool // Replace temporary pool handle with proper one from DB.
		}

		// Create any missing instance, storage volume, and storage bucket records.
		for projectName, poolVols := range poolsProjectVols[pool.Name()] {
			projectInfo := projects[projectName]

			if projectInfo == nil {
				// Shouldn't happen as we validated this above, but be sure for safety.
				return response.SmartError(fmt.Errorf("Project %q not found", projectName))
			}

			profileProjectName := project.ProfileProjectFromRecord(projectInfo)
			customStorageProjectName := project.StorageVolumeProjectFromRecord(projectInfo, dbCluster.StoragePoolVolumeTypeCustom)

			// Recover unknown custom volumes (do this first before recovering instances so that any
			// instances that reference unknown custom volume disk devices can be created).
			for _, poolVol := range poolVols {
				if poolVol.Instance != nil || poolVol.Bucket != nil {
					continue // Skip instance volumes and buckets.
				} else if poolVol.Instance == nil && len(poolVol.Volumes) == 0 {
					return response.SmartError(errors.New("Volume is neither instance nor custom volume"))
				}

				rootVol, err := poolVol.RootVolume()
				if err != nil {
					response.SmartError(fmt.Errorf("Failed getting the root volume: %w", err))
				}

				// Import custom volume and any snapshots.
				cleanup, err := pool.ImportCustomVolume(customStorageProjectName, poolVol, nil)
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed importing custom volume %q in project %q: %w", rootVol.Name, projectName, err))
				}

				revert.Add(cleanup)
			}

			// Recover unknown instance volumes.
			for _, poolVol := range poolVols {
				if poolVol.Instance == nil && (len(poolVol.Volumes) != 0 || poolVol.Bucket != nil) {
					continue // Skip custom volumes, invalid volumes and buckets.
				}

				// Recover instance volumes and any snapshots.
				profiles := make([]api.Profile, 0, len(poolVol.Instance.Profiles))
				for _, profileName := range poolVol.Instance.Profiles {
					for i := range projectProfiles[profileProjectName] {
						if projectProfiles[profileProjectName][i].Name == profileName {
							profiles = append(profiles, *projectProfiles[profileProjectName][i])
						}
					}
				}

				inst, cleanup, err := internalRecoverImportInstance(s, pool, projectName, poolVol, profiles)
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed creating instance %q record in project %q: %w", poolVol.Instance.Name, projectName, err))
				}

				revert.Add(cleanup)

				// Recover instance volume snapshots.
				for _, poolInstSnap := range poolVol.Snapshots {
					profiles := make([]api.Profile, 0, len(poolInstSnap.Profiles))
					for _, profileName := range poolInstSnap.Profiles {
						for i := range projectProfiles[profileProjectName] {
							if projectProfiles[profileProjectName][i].Name == profileName {
								profiles = append(profiles, *projectProfiles[profileProjectName][i])
							}
						}
					}

					cleanup, err := internalRecoverImportInstanceSnapshot(s, pool, projectName, poolVol, poolInstSnap, profiles)
					if err != nil {
						return response.SmartError(fmt.Errorf("Failed creating instance %q snapshot %q record in project %q: %w", poolVol.Instance.Name, poolInstSnap.Name, projectName, err))
					}

					revert.Add(cleanup)
				}

				// Recreate instance mount path and symlinks (must come after snapshot recovery).
				cleanup, err = pool.ImportInstance(inst, poolVol, nil)
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed importing instance %q in project %q: %w", poolVol.Instance.Name, projectName, err))
				}

				revert.Add(cleanup)

				// Reinitialise the instance's root disk quota even if no size specified (allows the storage driver the
				// opportunity to reinitialise the quota based on the new storage volume's DB ID).
				_, rootConfig, err := instancetype.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
				if err == nil {
					err = pool.SetInstanceQuota(inst, rootConfig["size"], rootConfig["size.state"], nil)
					if err != nil {
						return response.SmartError(fmt.Errorf("Failed reinitializing root disk quota %q for instance %q in project %q: %w", rootConfig["size"], poolVol.Instance.Name, projectName, err))
					}
				}
			}

			// Recover unknown buckets.
			for _, poolVol := range poolVols {
				// Skip non bucket volumes.
				if poolVol.Bucket == nil {
					continue
				}

				// Import bucket.
				cleanup, err := pool.ImportBucket(projectName, poolVol, nil)
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed importing bucket %q in project %q: %w", poolVol.Bucket.Name, projectName, err))
				}

				revert.Add(cleanup)
			}
		}
	}

	revert.Success()
	return response.EmptySyncResponse
}

// internalRecoverImportInstance recreates the database records for an instance and returns the new instance.
// Returns a revert fail function that can be used to undo this function if a subsequent step fails.
func internalRecoverImportInstance(s *state.State, pool storagePools.Pool, projectName string, poolVol *backupConfig.Config, profiles []api.Profile) (instance.Instance, revert.Hook, error) {
	if poolVol.Instance == nil {
		return nil, nil, errors.New("Pool volume is not an instance volume")
	}

	// Add root device if needed.
	if poolVol.Instance.Devices == nil {
		poolVol.Instance.Devices = make(map[string]map[string]string, 0)
	}

	if poolVol.Instance.ExpandedDevices == nil {
		poolVol.Instance.ExpandedDevices = make(map[string]map[string]string, 0)
	}

	internalImportRootDevicePopulate(pool.Name(), poolVol.Instance.Devices, poolVol.Instance.ExpandedDevices, profiles)

	dbInst, err := backup.ConfigToInstanceDBArgs(s, poolVol, projectName, true)
	if err != nil {
		return nil, nil, err
	}

	if dbInst.Type < 0 {
		return nil, nil, errors.New("Invalid instance type")
	}

	inst, instOp, cleanup, err := instance.CreateInternal(s, *dbInst, false)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed creating instance record: %w", err)
	}

	defer instOp.Done(err)

	return inst, cleanup, err
}

// internalRecoverImportInstance recreates the database records for an instance snapshot.
func internalRecoverImportInstanceSnapshot(s *state.State, pool storagePools.Pool, projectName string, poolVol *backupConfig.Config, snap *api.InstanceSnapshot, profiles []api.Profile) (revert.Hook, error) {
	if poolVol.Instance == nil || snap == nil {
		return nil, errors.New("Pool volume is not an instance volume")
	}

	// Add root device if needed.
	if snap.Devices == nil {
		snap.Devices = make(map[string]map[string]string, 0)
	}

	if snap.ExpandedDevices == nil {
		snap.ExpandedDevices = make(map[string]map[string]string, 0)
	}

	internalImportRootDevicePopulate(pool.Name(), snap.Devices, snap.ExpandedDevices, profiles)

	arch, err := osarch.ArchitectureId(snap.Architecture)
	if err != nil {
		return nil, err
	}

	instanceType, err := instancetype.New(poolVol.Instance.Type)
	if err != nil {
		return nil, err
	}

	snapshotExpiry := snap.Config["snapshots.expiry"]
	if snapshotExpiry != "" {
		expiry, err := shared.GetExpiry(snap.CreatedAt, snapshotExpiry)
		if err != nil {
			return nil, err
		}

		snap.ExpiresAt = expiry
	}

	_, snapInstOp, cleanup, err := instance.CreateInternal(s, db.InstanceArgs{
		Project:      projectName,
		Architecture: arch,
		BaseImage:    snap.Config["volatile.base_image"],
		Config:       snap.Config,
		CreationDate: snap.CreatedAt,
		ExpiryDate:   snap.ExpiresAt,
		Type:         instanceType,
		Snapshot:     true,
		Devices:      deviceConfig.NewDevices(snap.Devices),
		Ephemeral:    snap.Ephemeral,
		LastUsedDate: snap.LastUsedAt,
		Name:         poolVol.Instance.Name + shared.SnapshotDelimiter + snap.Name,
		Profiles:     profiles,
		Stateful:     snap.Stateful,
	}, false)
	if err != nil {
		return nil, fmt.Errorf("Failed creating instance snapshot record %q: %w", snap.Name, err)
	}

	defer snapInstOp.Done(err)

	return cleanup, err
}

// internalRecoverValidate validates the requested pools to be recovered.
func internalRecoverValidate(d *Daemon, r *http.Request) response.Response {
	// Parse the request.
	req := &internalRecoverValidatePost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	return internalRecoverScan(r.Context(), d.State(), req.Pools, true)
}

// internalRecoverImport performs the pool volume recovery.
func internalRecoverImport(d *Daemon, r *http.Request) response.Response {
	// Parse the request.
	req := &internalRecoverImportPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	return internalRecoverScan(r.Context(), d.State(), req.Pools, false)
}
