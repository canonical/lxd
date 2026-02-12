package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/mux"

	"github.com/canonical/lxd/lxd/auth"
	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db"
	dbCluster "github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/project/limits"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/version"
)

// swagger:operation POST /1.0/instances/{name} instances instance_post
//
//	Rename or move/migrate an instance
//
//	Renames, moves an instance between pools or migrates an instance to another server.
//
//	The returned operation metadata will vary based on what's requested.
//	For rename or move within the same server, this is a simple background operation with progress data.
//	For migration, in the push case, this will similarly be a background
//	operation with progress data, for the pull case, it will be a websocket
//	operation with a number of secrets to be passed to the target server.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: migration
//	    description: Migration request
//	    schema:
//	      $ref: "#/definitions/InstancePost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instancePost(d *Daemon, r *http.Request) response.Response {
	// Don't mess with instance while in setup mode.
	<-d.waitReady.Done()

	s := d.State()
	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return response.SmartError(err)
	}

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(name) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	// Flag indicating whether the node running the instance is offline.
	sourceNodeOffline := false

	var targetProject *api.Project
	var targetMemberInfo *db.NodeInfo
	var candidateMembers []db.NodeInfo

	target := request.QueryParam(r, "target")
	if !s.ServerClustered && target != "" {
		return response.BadRequest(errors.New("Target only allowed when clustered"))
	}

	// A POST to /instances/<name>?target=<member> is meant to be used to
	// move an instance from one member to another within a cluster.
	//
	// Determine if either the source node (the one currently
	// running the instance) or the target node are offline.
	//
	// If the target node is offline, we return an error.
	//
	// If the source node is offline and the instance is backed by
	// ceph, we'll just assume that the instance is not running
	// and it's safe to move it.
	//
	// TODO: add some sort of "force" flag to the API, to signal
	//       that the user really wants to move the instance even
	//       if we can't know for sure that it's indeed not
	//       running?
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Load source node.
		sourceAddress, err := tx.GetNodeAddressOfInstance(ctx, projectName, name, instanceType)
		if err != nil {
			return fmt.Errorf("Failed getting address of instance's member: %w", err)
		}

		if sourceAddress == "" {
			// Local node.
			sourceNodeOffline = false
			return nil
		}

		sourceMemberInfo, err := tx.GetNodeByAddress(ctx, sourceAddress)
		if err != nil {
			return fmt.Errorf("Failed getting source member for %q: %w", sourceAddress, err)
		}

		sourceNodeOffline = sourceMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold())

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Check whether to forward the request to the node that is running the
	// instance. Here are the possible cases:
	//
	// 1. No "?target=<member>" parameter was passed. In this case this is
	//    just an instance rename, with no move, and we want the request to be
	//    handled by the node which is actually running the instance.
	//
	// 2. The "?target=<member>" parameter was set and the node running the
	//    instance is online. In this case we want to forward the request to
	//    that node, which might do things like unmapping the RBD volume for
	//    ceph instances.
	//
	// 3. The "?target=<member>" parameter was set but the node running the
	//    instance is offline. We don't want to forward to the request to
	//    that node and we don't want to load the instance here (since
	//    it's not a local instance): we'll be able to handle the request
	//    at all only if the instance is backed by ceph. We'll check for
	//    that just below.
	//
	// Cases 1. and 2. are the ones for which the conditional will be true
	// and we'll either forward the request or load the instance.
	if target == "" || !sourceNodeOffline {
		// Handle requests targeted to an instance on a different node.
		resp, err := forwardedResponseIfInstanceIsRemote(r.Context(), s, projectName, name, instanceType)
		if err != nil {
			return response.SmartError(err)
		}

		if resp != nil {
			return resp
		}
	} else if sourceNodeOffline {
		// If a target was specified, forward the request to the relevant node.
		target := request.QueryParam(r, "target")
		resp := forwardedResponseToNode(r.Context(), s, target)
		if resp != nil {
			return resp
		}
	}

	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	rdr1 := io.NopCloser(bytes.NewBuffer(body))
	rdr2 := io.NopCloser(bytes.NewBuffer(body))

	reqRaw := shared.Jmap{}
	err = json.NewDecoder(rdr1).Decode(&reqRaw)
	if err != nil {
		return response.BadRequest(err)
	}

	req := api.InstancePost{}
	err = json.NewDecoder(rdr2).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check if stateful indicator supplied and default to true if not (for backward compatibility).
	_, err = reqRaw.GetBool("live")
	if err != nil {
		req.Live = true
	}

	// If new instance name not supplied, assume it will be keeping its current name.
	if req.Name == "" {
		req.Name = inst.Name()
	}

	// Check the new instance name is valid.
	err = instancetype.ValidName(req.Name, false)
	if err != nil {
		return response.BadRequest(err)
	}

	var targetGroupName string
	after, ok := strings.CutPrefix(target, instancetype.TargetClusterGroupPrefix)
	if ok {
		targetGroupName = after
	}

	targetProjectName := req.Project
	if targetProjectName == "" {
		targetProjectName = inst.Project().Name
	}

	// Run the cluster placement after potentially forwarding the request to another member.
	if target != "" && s.ServerClustered {
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			p, err := dbCluster.GetProject(ctx, tx.Tx(), targetProjectName)
			if err != nil {
				return err
			}

			targetProject, err = p.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			allMembers, err := tx.GetNodes(ctx)
			if err != nil {
				return fmt.Errorf("Failed getting cluster members: %w", err)
			}

			targetMemberInfo, targetGroupName, err = limits.CheckTarget(ctx, s.Authorizer, tx, targetProject, target, allMembers)
			if err != nil {
				return err
			}

			if targetMemberInfo == nil {
				clusterGroupsAllowed := limits.GetRestrictedClusterGroups(targetProject)

				candidateMembers, err = tx.GetCandidateMembers(ctx, allMembers, []int{inst.Architecture()}, targetGroupName, clusterGroupsAllowed, s.GlobalConfig.OfflineThreshold())
				if err != nil {
					return err
				}
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		// Pick the member with the least number of instances.
		if targetMemberInfo == nil {
			var filteredCandidateMembers []db.NodeInfo

			// The instance might already be placed on the node with least number of instances.
			// Therefore remove it from the list of possible candidates if existent.
			for _, candidateMember := range candidateMembers {
				if candidateMember.Name != inst.Location() {
					filteredCandidateMembers = append(filteredCandidateMembers, candidateMember)
				}
			}

			err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
				targetMemberInfo, err = tx.GetNodeWithLeastInstances(ctx, filteredCandidateMembers)
				return err
			})
			if err != nil {
				return response.SmartError(err)
			}
		}

		if targetMemberInfo != nil && targetMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold()) {
			return response.BadRequest(errors.New("Target cluster member is offline"))
		}
	}

	// Unset "volatile.cluster.group" if the instance is manually moved to a cluster member.
	if targetMemberInfo != nil && targetGroupName == "" && inst.LocalConfig()["volatile.cluster.group"] != "" {
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			err = tx.DeleteInstanceConfigKey(ctx, int64(inst.ID()), "volatile.cluster.group")
			if err != nil {
				return fmt.Errorf(`Failed removing "volatile.cluster.group" config key: %w`, err)
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	if req.Migration {
		// Server-side instance migration.
		hasConfigOverrides := req.Config != nil || req.Devices != nil || req.Profiles != nil || req.OverrideSnapshotProfiles
		hasInstanceChanges := req.Pool != "" || targetProjectName != inst.Project().Name || hasConfigOverrides

		// Check if user has access to target project when changing projects.
		if targetProjectName != inst.Project().Name {
			err := s.Authorizer.CheckPermission(r.Context(), entity.ProjectURL(req.Project), auth.EntitlementCanCreateInstances)
			if err != nil {
				return response.SmartError(err)
			}
		}

		// needsClusterMove determines if we need to migrate the instance to a different cluster member.
		// This is true when a target member is specified and any of the following conditions are met:
		// - The target member is different from the current location.
		// - A cluster group needs to be set.
		// - There are instance changes to apply (pool, project, config, devices, or profiles).
		needsClusterMove := targetMemberInfo != nil &&
			(inst.Location() != targetMemberInfo.Name || targetGroupName != "" || hasInstanceChanges)

		// needsLocalCopy determines if we need to copy the instance locally on the current member before migrating.
		// A local copy creates a new instance on the same member with the desired changes applied.
		// This is a two-phase operation: first copy locally with changes, then migrate the new instance to the target.
		//
		// When migrating within a cluster (needsClusterMove), the cluster migration protocol can apply
		// pool/config/device/profile changes directly during the migration itself, so only project changes
		// require a local copy (the cluster migration protocol assumes the same project on both sides).
		//
		// For all other cases (non-cluster migrations or no target specified), any instance changes
		// (pool/project/config/devices/profiles) require a local copy to be applied first.
		needsLocalCopy := hasInstanceChanges
		if needsClusterMove {
			needsLocalCopy = targetProjectName != inst.Project().Name
		}

		// Validate offline source member constraints.
		if needsClusterMove && sourceNodeOffline {
			srcPool, err := storagePools.LoadByInstance(s, inst)
			if err != nil {
				return response.InternalError(fmt.Errorf("Failed loading instance storage pool: %w", err))
			}

			if srcPool.Driver().Info().Remote {
				// Remote storage with offline source only supports name changes, not pool/config/profile changes.
				if req.Pool != "" && req.Pool != srcPool.Name() {
					return response.BadRequest(errors.New("Pool changes are not supported when moving remote storage instances from an offline member"))
				}

				if targetProjectName != inst.Project().Name {
					return response.BadRequest(errors.New("Project changes are not supported when moving remote storage instances from an offline member"))
				}

				if hasConfigOverrides {
					return response.BadRequest(errors.New("Configuration changes are not supported when moving remote storage instances from an offline member"))
				}
			}
		}

		if needsLocalCopy || needsClusterMove {
			if needsClusterMove {
				var backups []string

				// Check if instance has backups.
				err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
					backups, err = tx.GetInstanceBackups(ctx, projectName, name)
					return err
				})
				if err != nil {
					err = fmt.Errorf("Failed fetching instance's backups: %w", err)
					return response.SmartError(err)
				}

				if len(backups) > 0 {
					return response.BadRequest(errors.New("Instance has backups"))
				}
			}

			finalName := req.Name
			if finalName == "" {
				finalName = inst.Name()
			}

			// Setup the instance move operation.
			run := func(ctx context.Context, op *operations.Operation) error {
				currentInst := inst

				// Handle local changes.
				if needsLocalCopy {
					// Pass a nil target member so the local copy phase never triggers a cluster move.
					err := instancePostMigration(ctx, s, currentInst, req, nil, "", op)
					if err != nil {
						return err
					}

					reloadProject := targetProjectName
					if reloadProject == "" {
						reloadProject = currentInst.Project().Name
					}

					currentInst, err = instance.LoadByProjectAndName(s, reloadProject, finalName)
					if err != nil {
						return err
					}
				}

				if needsClusterMove {
					// Handle cluster move phase, including any pool/project/config changes.
					return instancePostMigration(ctx, s, currentInst, req, targetMemberInfo, targetGroupName, op)
				}

				return nil
			}

			instanceURL := api.NewURL().Path(version.APIVersion, "instances", name).Project(projectName)
			resources := map[entity.Type][]api.URL{
				entity.TypeInstance: {*instanceURL},
			}

			args := operations.OperationArgs{
				ProjectName: projectName,
				EntityURL:   instanceURL,
				Type:        operationtype.InstanceMigrate,
				Class:       operations.OperationClassTask,
				Resources:   resources,
				RunHook:     run,
			}

			op, err := operations.CreateUserOperation(s, requestor, args)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		// We keep the req.ContainerOnly for backward compatibility.
		instanceOnly := req.InstanceOnly || req.ContainerOnly //nolint:staticcheck,unused
		ws, err := newMigrationSource(inst, req.Live, instanceOnly, req.AllowInconsistent, "", req.Target)
		if err != nil {
			return response.InternalError(err)
		}

		resources := map[entity.Type][]api.URL{
			entity.TypeInstance: {*api.NewURL().Path(version.APIVersion, "instances", name).Project(projectName)},
		}

		run := func(ctx context.Context, op *operations.Operation) error {
			// Migrations do not currently cancel via context.
			// The only way to cancel them is by disconnecting the websocket.
			// This goroutine disconnects the migration websocket if the context is cancelled before the migration is complete.
			done := make(chan struct{})
			defer close(done)
			go func() {
				select {
				case <-done:
					return
				case <-ctx.Done():
					ws.disconnect()
				}
			}()

			return ws.Do(s, op)
		}

		if req.Target != nil {
			// Push mode.
			args := operations.OperationArgs{
				ProjectName: projectName,
				EntityURL:   api.NewURL().Path(version.APIVersion, "instances", name).Project(projectName),
				Type:        operationtype.InstanceMigrate,
				Class:       operations.OperationClassTask,
				Resources:   resources,
				RunHook:     run,
			}

			op, err := operations.CreateUserOperation(s, requestor, args)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		// Pull mode.
		args := operations.OperationArgs{
			ProjectName: projectName,
			EntityURL:   api.NewURL().Path(version.APIVersion, "instances", name).Project(projectName),
			Type:        operationtype.InstanceMigrate,
			Class:       operations.OperationClassWebsocket,
			Resources:   resources,
			Metadata:    ws.Metadata(),
			RunHook:     run,
			ConnectHook: ws.Connect,
		}

		op, err := operations.CreateUserOperation(s, requestor, args)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	var id int

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the name isn't already in use.
		id, _ = tx.GetInstanceID(ctx, projectName, req.Name)

		return nil
	})
	if id > 0 {
		return response.Conflict(fmt.Errorf("Name %q already in use", req.Name))
	}

	run := func(context.Context, *operations.Operation) error {
		return inst.Rename(req.Name, true)
	}

	resources := map[entity.Type][]api.URL{
		entity.TypeInstance: {*api.NewURL().Path(version.APIVersion, "instances", name).Project(projectName)},
	}

	args := operations.OperationArgs{
		ProjectName: projectName,
		EntityURL:   api.NewURL().Path(version.APIVersion, "instances", name).Project(projectName),
		Type:        operationtype.InstanceRename,
		Class:       operations.OperationClassTask,
		Resources:   resources,
		RunHook:     run,
	}

	op, err := operations.CreateUserOperation(s, requestor, args)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// Move an instance.
func instancePostMigration(ctx context.Context, s *state.State, inst instance.Instance, req api.InstancePost, targetMemberInfo *db.NodeInfo, targetGroupName string, op *operations.Operation) error {
	if inst.IsSnapshot() {
		return errors.New("Instance snapshots cannot be moved between pools")
	}

	sourceName := inst.Name()
	sourceProject := inst.Project().Name

	if req.Project == "" {
		req.Project = sourceProject
	}

	if req.Name == "" {
		req.Name = sourceName
	}

	// Copy config from instance to avoid modifying it.
	localConfig := make(map[string]string)
	maps.Copy(localConfig, inst.LocalConfig())

	// Set user defined configuration entries.
	maps.Copy(localConfig, req.Config)

	// Get instance local devices and then set user defined devices.
	localDevices := inst.LocalDevices().Clone()
	for devName, dev := range req.Devices {
		localDevices[devName] = dev
	}

	// Apply previous profiles, if provided profiles are nil.
	if req.Profiles == nil {
		for _, p := range inst.Profiles() {
			req.Profiles = append(req.Profiles, p.Name)
		}
	}

	apiProfiles := []api.Profile{}
	if len(req.Profiles) > 0 {
		err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			profiles, err := dbCluster.GetProfilesIfEnabled(ctx, tx.Tx(), req.Project, req.Profiles)
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

			apiProfiles = make([]api.Profile, 0, len(profiles))
			for _, profile := range profiles {
				apiProfile, err := profile.ToAPI(ctx, tx.Tx(), profileConfigs, profileDevices)
				if err != nil {
					return err
				}

				apiProfiles = append(apiProfiles, *apiProfile)
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	// Check if root disk device is present in the instance config. If instance config has no
	// root disk device configured, check if the same root disk device will be applied with new
	// profiles in the target project. If the new root disk device differs from the existing
	// one, add the existing one as a local device to the instance (we don't want to move root
	// disk device if not necessary, as this is an expensive operation).
	rootDevKey, rootDev, err := instancetype.GetRootDiskDevice(localDevices.CloneNative())
	if err != nil && !errors.Is(err, instancetype.ErrNoRootDisk) {
		return err
	} else if errors.Is(err, instancetype.ErrNoRootDisk) {
		// Find currently applied root disk device from expanded devices.
		rootDevKey, rootDev, err = instancetype.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
		if err != nil {
			return err
		}

		// Iterate over profiles that will be applied in the target project and find
		// the new root disk device. Iterate in reverse order to respect profile
		// precedence.
		var profileRootDev map[string]string
		for i := len(apiProfiles) - 1; i >= 0; i-- {
			_, profileRootDev, err = instancetype.GetRootDiskDevice(apiProfiles[i].Devices)
			if err == nil {
				break
			}
		}

		// If current root disk device would be replaced according to the new profiles,
		// add current root disk device to local instance devices (to retain it).
		if profileRootDev == nil ||
			profileRootDev["pool"] != rootDev["pool"] ||
			profileRootDev["size"] != rootDev["size"] ||
			profileRootDev["size.state"] != rootDev["size.state"] {
			localDevices[rootDevKey] = rootDev
		}
	}

	// Set specific storage pool for the instance, if provided.
	if req.Pool != "" {
		rootDev["pool"] = req.Pool
		localDevices[rootDevKey] = rootDev
	}

	// Specify the target instance config with the new name and project.
	targetArgs := db.InstanceArgs{
		Name:         req.Name,
		BaseImage:    localConfig["volatile.base_image"],
		Config:       localConfig,
		Devices:      localDevices,
		Profiles:     apiProfiles,
		Project:      req.Project,
		Type:         inst.Type(),
		Architecture: inst.Architecture(),
		Description:  inst.Description(),
		Ephemeral:    inst.IsEphemeral(),
		Stateful:     inst.IsStateful(),
	}

	if targetMemberInfo != nil {
		return migrateInstance(ctx, s, inst, targetMemberInfo.Name, targetGroupName, req, &targetArgs, op)
	}

	statefulStart := false
	if inst.IsRunning() {
		if !req.Live {
			return api.StatusErrorf(http.StatusBadRequest, "Instance must be stopped to move between pools statelessly")
		}

		statefulStart = true
		err := inst.Stop(true)
		if err != nil {
			return err
		}
	}

	tempNameRequired := req.Name == sourceName && req.Project == sourceProject
	if tempNameRequired {
		targetArgs.Name, err = instance.MoveTemporaryName(inst)
		if err != nil {
			return err
		}
	}

	// Copy instance to new target instance.
	targetInst, err := instanceCreateAsCopy(s, instanceCreateAsCopyOpts{
		sourceInstance:           inst,
		targetInstance:           targetArgs,
		instanceOnly:             req.InstanceOnly,
		applyTemplateTrigger:     false, // Don't apply templates when moving.
		allowInconsistent:        req.AllowInconsistent,
		overrideSnapshotProfiles: req.OverrideSnapshotProfiles,
	}, op)
	if err != nil {
		return err
	}

	// Update any permissions relating to the old instance to point to the new instance before it is deleted.
	// Warnings relating to the old instance will be deleted.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		q := `UPDATE auth_groups_permissions SET entity_id = ? WHERE entity_type = ? AND entity_id = ?`
		_, err = tx.Tx().ExecContext(ctx, q, targetInst.ID(), dbCluster.EntityType(entity.TypeInstance), inst.ID())
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed copying instance permissions: %w", err)
	}

	// Delete original instance.
	err = inst.Delete(true, "")
	if err != nil {
		return err
	}

	// Rename copy from temporary name to original name if needed.
	if tempNameRequired {
		err = targetInst.Rename(req.Name, false) // Don't apply templates when moving.
		if err != nil {
			return err
		}
	}

	if statefulStart {
		err = targetInst.Start(true)
		if err != nil {
			return err
		}
	}

	return nil
}

// Migrate an instance to another cluster node (supports both local and remote storage).
// Source and target members must be online.
func instancePostClusteringMigrate(ctx context.Context, s *state.State, srcPool storagePools.Pool, srcInst instance.Instance, req api.InstancePost, targetArgs *db.InstanceArgs, srcMember db.NodeInfo, newMember db.NodeInfo, targetGroupName string) (func(ctx context.Context, op *operations.Operation) error, error) {
	srcMemberOffline := srcMember.IsOffline(s.GlobalConfig.OfflineThreshold())

	// Make sure that the source member is online if we end up being called from another member after a
	// redirection due to the source member being offline.
	if srcMemberOffline {
		return nil, errors.New("The cluster member hosting the instance is offline")
	}

	// Make sure that the destination member is not in evacuated state.
	if newMember.State == db.ClusterMemberStateEvacuated {
		return nil, errors.New("The destination cluster member is evacuated")
	}

	stateful := req.Live
	allowInconsistent := req.AllowInconsistent

	// Check we can convert the instance to the volume types needed.
	volType, err := storagePools.InstanceTypeToVolumeType(srcInst.Type())
	if err != nil {
		return nil, err
	}

	volDBType, err := storagePools.VolumeTypeToDBType(volType)
	if err != nil {
		return nil, err
	}

	newInstName := req.Name
	if targetArgs != nil && targetArgs.Name != "" {
		newInstName = targetArgs.Name
	}

	srcInstName := srcInst.Name()
	if newInstName == "" {
		newInstName = srcInstName
	}

	targetProject := srcInst.Project().Name
	if targetArgs != nil && targetArgs.Project != "" {
		targetProject = targetArgs.Project
	}

	var targetProfileNames []string
	if targetArgs != nil && len(targetArgs.Profiles) > 0 {
		targetProfileNames = make([]string, 0, len(targetArgs.Profiles))
		for _, profile := range targetArgs.Profiles {
			targetProfileNames = append(targetProfileNames, profile.Name)
		}
	}

	run := func(ctx context.Context, op *operations.Operation) error {
		networkCert := s.Endpoints.NetworkCert()

		// Connect to the destination member, i.e. the member to migrate the instance to.
		// Use the notify argument to indicate to the destination that we are moving an instance between
		// cluster members.
		dest, err := cluster.Connect(ctx, newMember.Address, networkCert, s.ServerCert(), true)
		if err != nil {
			return fmt.Errorf("Failed connecting to destination server %q: %w", newMember.Address, err)
		}

		dest = dest.UseTarget(newMember.Name).UseProject(targetProject)
		resources := map[entity.Type][]api.URL{
			entity.TypeInstance: {*api.NewURL().Path(version.APIVersion, "instances", srcInstName).Project(srcInst.Project().Name)},
		}

		srcInstRunning := srcInst.IsRunning()
		live := stateful && srcInstRunning

		// During a stateful migration we expect the migration process to stop the instance on the source
		// once the migration is complete. However when doing a stateless migration and the instance is
		// running we must forcefully stop the instance on the source before starting the migration copy
		// so that it is as consistent as possible.
		if !stateful && srcInstRunning {
			err := srcInst.Stop(false)
			if err != nil {
				return fmt.Errorf("Failed statelessly stopping instance %q: %w", srcInstName, err)
			}
		}

		// Rename instance if requested.
		if newInstName != srcInstName {
			err = srcInst.Rename(newInstName, true)
			if err != nil {
				return fmt.Errorf("Failed renaming instance %q to %q: %w", srcInstName, newInstName, err)
			}

			srcInst, err = instance.LoadByProjectAndName(s, targetProject, newInstName)
			if err != nil {
				return fmt.Errorf("Failed loading renamed instance: %w", err)
			}

			srcInstName = srcInst.Name()
		}

		snapshots, err := srcInst.Snapshots()
		if err != nil {
			return fmt.Errorf("Failed getting source instance snapshots: %w", err)
		}

		// Setup migration source.
		srcRenderRes, _, err := srcInst.Render()
		if err != nil {
			return fmt.Errorf("Failed getting source instance info: %w", err)
		}

		srcInstInfo, ok := srcRenderRes.(*api.Instance)
		if !ok {
			return fmt.Errorf("Unexpected result from source instance render: %w", err)
		}

		if targetArgs != nil {
			srcInstInfo.Project = targetProject
			srcInstInfo.Config = targetArgs.Config
			srcInstInfo.Devices = targetArgs.Devices.CloneNative()
			srcInstInfo.Description = targetArgs.Description
			srcInstInfo.Ephemeral = targetArgs.Ephemeral
			srcInstInfo.Stateful = targetArgs.Stateful
			if len(targetProfileNames) > 0 {
				srcInstInfo.Profiles = targetProfileNames
			}
		}

		srcMigration, err := newMigrationSource(srcInst, live, false, allowInconsistent, srcInstName, nil)
		if err != nil {
			return fmt.Errorf("Failed setting up instance migration on source: %w", err)
		}

		run := func(ctx context.Context, op *operations.Operation) error {
			// Migrations do not currently cancel via context.
			// The only way to cancel them is by disconnecting the websocket.
			// This goroutine disconnects the migration websocket if the context is cancelled before the migration is complete.
			done := make(chan struct{})
			defer close(done)
			go func() {
				select {
				case <-done:
					return
				case <-ctx.Done():
					srcMigration.disconnect()
				}
			}()

			return srcMigration.Do(s, op)
		}

		args := operations.OperationArgs{
			ProjectName: targetProject,
			EntityURL:   api.NewURL().Path(version.APIVersion, "instances", srcInstName).Project(srcInst.Project().Name),
			Type:        operationtype.InstanceMigrate,
			Class:       operations.OperationClassWebsocket,
			Resources:   resources,
			Metadata:    srcMigration.Metadata(),
			RunHook:     run,
			ConnectHook: srcMigration.Connect,
		}

		srcOp, err := operations.CreateUserOperation(s, op.Requestor(), args)
		if err != nil {
			return err
		}

		err = srcOp.Start()
		if err != nil {
			return fmt.Errorf("Failed starting migration source operation: %w", err)
		}

		sourceSecrets := make(map[string]string, len(srcMigration.conns))
		for connName, conn := range srcMigration.conns {
			sourceSecrets[connName] = conn.Secret()
		}

		// Request pull mode migration on destination.
		destOp, err := dest.CreateInstance(api.InstancesPost{
			Name:        newInstName,
			InstancePut: srcInstInfo.Writable(),
			Type:        api.InstanceType(srcInstInfo.Type),
			Source: api.InstanceSource{
				Type:        api.SourceTypeMigration,
				Mode:        "pull",
				Operation:   "https://" + srcMember.Address + srcOp.URL(),
				Websockets:  sourceSecrets,
				Certificate: string(networkCert.PublicKey()),
				Live:        live,
				Source:      srcInstName,
			},
		})
		if err != nil {
			return fmt.Errorf("Failed requesting instance create on destination: %w", err)
		}

		handler := func(newOp api.Operation) {
			_ = op.UpdateMetadata(newOp.Metadata)
		}

		_, err = destOp.AddHandler(handler)
		if err != nil {
			return err
		}

		err = srcOp.Wait(context.Background())
		if err != nil {
			return fmt.Errorf("Instance move to destination failed on source: %w", err)
		}

		err = destOp.Wait()
		if err != nil {
			return fmt.Errorf("Instance move to destination failed: %w", err)
		}

		err = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Update instance DB record to indicate its location on the new cluster member.
			err = tx.UpdateInstanceNode(ctx, targetProject, srcInstName, newInstName, srcInst.ID(), newMember.Name, srcPool.ID(), volDBType)
			if err != nil {
				return fmt.Errorf("Failed updating cluster member to %q for instance %q: %w", newMember.Name, newInstName, err)
			}

			// Set the cluster group record if needed.
			if targetGroupName != "" {
				err = tx.UpdateInstanceConfig(srcInst.ID(), map[string]string{"volatile.cluster.group": targetGroupName})
				if err != nil {
					return fmt.Errorf(`Failed setting "volatile.cluster.group" config key: %w`, err)
				}
			}

			return nil
		})
		if err != nil {
			return err
		}

		// Cleanup instance paths on source member if using remote shared storage.
		if srcPool.Driver().Info().Remote {
			err = srcPool.CleanupInstancePaths(srcInst, nil)
			if err != nil {
				return fmt.Errorf("Failed cleaning up instance paths on source member: %w", err)
			}
		} else {
			// Delete the instance on source member if pool isn't remote shared storage.
			// We cannot use the normal delete process, as that would remove the instance DB record.
			// So instead we need to delete just the local storage volume(s) for the instance.
			snapshotCount := len(snapshots)
			for k := range snapshots {
				// Delete the snapshots in reverse order.
				k = snapshotCount - 1 - k

				err = srcPool.DeleteInstanceSnapshot(snapshots[k], nil)
				if err != nil {
					return fmt.Errorf("Failed delete instance snapshot %q on source member: %w", snapshots[k].Name(), err)
				}
			}

			err = srcPool.DeleteInstance(srcInst, nil)
			if err != nil {
				return fmt.Errorf("Failed deleting instance on source member: %w", err)
			}
		}

		if !stateful && srcInstRunning {
			req := api.InstanceStatePut{
				Action: "start",
			}

			op, err := dest.UpdateInstanceState(newInstName, req, "")
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return fmt.Errorf("Failed starting instance %q: %w", newInstName, err)
			}
		}

		return nil
	}

	return run, nil
}

// instancePostClusteringMigrateWithRemoteStorage handles moving a remote shared storage instance from a source member that is offline.
// This function must be run on the target cluster member to move the instance to.
func instancePostClusteringMigrateWithRemoteStorage(s *state.State, srcPool storagePools.Pool, srcInst instance.Instance, newInstName string, newMember db.NodeInfo, targetGroupName string) (func(ctx context.Context, op *operations.Operation) error, error) {
	// Sense checks to avoid unexpected behaviour.
	if !srcPool.Driver().Info().Remote {
		return nil, errors.New("Source instance's storage pool is not remote shared storage")
	}

	// Check this function is only run on the target member.
	if s.ServerName != newMember.Name {
		return nil, errors.New("Remote shared storage instance move when source member is offline must be run on target member")
	}

	finalName := srcInst.Name()
	if newInstName != "" {
		finalName = newInstName
	}

	// Check we can convert the instance to the volume types needed.
	volType, err := storagePools.InstanceTypeToVolumeType(srcInst.Type())
	if err != nil {
		return nil, err
	}

	volDBType, err := storagePools.VolumeTypeToDBType(volType)
	if err != nil {
		return nil, err
	}

	run := func(ctx context.Context, op *operations.Operation) error {
		projectName := srcInst.Project().Name
		srcInstName := srcInst.Name()

		// Re-link the database entries against the new member name.
		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			err := tx.UpdateInstanceNode(ctx, projectName, srcInstName, finalName, srcInst.ID(), newMember.Name, srcPool.ID(), volDBType)
			if err != nil {
				return fmt.Errorf("Failed updating cluster member to %q for instance %q: %w", newMember.Name, finalName, err)
			}

			return nil
		})
		if err != nil {
			return fmt.Errorf("Failed relinking instance database data: %w", err)
		}

		if srcInstName != finalName {
			err = srcInst.Rename(finalName, true)
			if err != nil {
				return fmt.Errorf("Failed renaming instance %q to %q: %w", srcInstName, finalName, err)
			}

			srcInst, err = instance.LoadByProjectAndName(s, projectName, finalName)
			if err != nil {
				return fmt.Errorf("Failed loading renamed instance: %w", err)
			}

			srcInstName = srcInst.Name()
		}

		_, err = srcPool.ImportInstance(srcInst, nil, nil)
		if err != nil {
			return fmt.Errorf("Failed creating mount point of instance on target node: %w", err)
		}

		// Record the cluster group record if needed.
		if targetGroupName != "" {
			err = srcInst.VolatileSet(map[string]string{"volatile.cluster.group": targetGroupName})
			if err != nil {
				return err
			}
		}

		return nil
	}

	return run, nil
}

func migrateInstance(ctx context.Context, s *state.State, inst instance.Instance, targetNode string, targetGroupName string, req api.InstancePost, targetArgs *db.InstanceArgs, op *operations.Operation) error {
	// If target isn't the same as the instance's location.
	if targetNode == inst.Location() {
		return errors.New("Target must be different than instance's current location")
	}

	var err error
	var srcMember, newMember db.NodeInfo

	// If the source member is online then get its address so we can connect to it and see if the
	// instance is running later.
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		srcMember, err = tx.GetNodeByName(ctx, inst.Location())
		if err != nil {
			return fmt.Errorf("Failed getting current cluster member of instance %q", inst.Name())
		}

		newMember, err = tx.GetNodeByName(ctx, targetNode)
		if err != nil {
			return fmt.Errorf("Failed loading new cluster member for instance: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Retrieve storage pool of the source instance.
	srcPool, err := storagePools.LoadByInstance(s, inst)
	if err != nil {
		return fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	// Only use instancePostClusteringMigrateWithRemoteStorage when source member is offline and storage location is remote.
	if srcMember.IsOffline(s.GlobalConfig.OfflineThreshold()) && srcPool.Driver().Info().Remote {
		newInstName := req.Name
		if targetArgs != nil && targetArgs.Name != "" {
			newInstName = targetArgs.Name
		}

		if newInstName == "" {
			newInstName = inst.Name()
		}

		f, err := instancePostClusteringMigrateWithRemoteStorage(s, srcPool, inst, newInstName, newMember, targetGroupName)
		if err != nil {
			return err
		}

		return f(ctx, op)
	}

	f, err := instancePostClusteringMigrate(ctx, s, srcPool, inst, req, targetArgs, srcMember, newMember, targetGroupName)
	if err != nil {
		return err
	}

	return f(ctx, op)
}
