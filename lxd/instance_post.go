package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"

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
	"github.com/canonical/lxd/lxd/scriptlet"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	apiScriptlet "github.com/canonical/lxd/shared/api/scriptlet"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/logger"
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

	// Parse the request URL.
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	target := request.QueryParam(r, "target")

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Quick checks.
	if shared.IsSnapshot(name) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	s := d.State()

	if target != "" && !s.ServerClustered {
		return response.BadRequest(errors.New("Target only allowed when clustered"))
	}

	// Check if the server the instance is running on is currently online.
	var sourceMemberInfo *db.NodeInfo
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Load source node.
		sourceAddress, err := tx.GetNodeAddressOfInstance(ctx, projectName, name, instanceType)
		if err != nil {
			return fmt.Errorf("Failed to get address of instance's member: %w", err)
		}

		if sourceAddress == "" {
			// Local node.
			return nil
		}

		info, err := tx.GetNodeByAddress(ctx, sourceAddress)
		if err != nil {
			return fmt.Errorf("Failed to get source member for %q: %w", sourceAddress, err)
		}

		sourceMemberInfo = &info

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// More checks.
	if target == "" && sourceMemberInfo != nil && sourceMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold()) {
		return response.BadRequest(errors.New("Can't perform action as server is currently offline"))
	}

	// Handle request forwarding.
	if sourceMemberInfo != nil && sourceMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold()) {
		// Current location of the instance isn't available and we've been asked to relocate it, forward to target.
		// If the member name is empty or matches the current cluster member, nil is returned and no forwarding is required.
		resp := forwardedResponseToNode(r.Context(), s, target)
		if resp != nil {
			return resp
		}
	} else if target == "" || sourceMemberInfo == nil || !sourceMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold()) {
		// Forward the request to the instance's current location (if not local).
		resp, err := forwardedResponseIfInstanceIsRemote(r.Context(), s, projectName, name, instanceType)
		if err != nil {
			return response.SmartError(err)
		}

		if resp != nil {
			return resp
		}
	}

	// Parse the request.
	req := api.InstancePost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Target instance properties.
	instProject := projectName
	instLocation := target

	// Clear instance name if it's the same.
	// This skips instance name validation and allows the instance to keep its current name.
	if req.Name != "" && req.Name == name {
		req.Name = ""
	}

	// Validate the new target project (if provided).
	if req.Project != "" {
		// Confirm access to target project.
		err := s.Authorizer.CheckPermission(r.Context(), entity.ProjectURL(req.Project), auth.EntitlementCanCreateInstances)
		if err != nil {
			return response.SmartError(err)
		}

		instProject = req.Project
	}

	// Validate the new instance name (if provided).
	if req.Name != "" {
		// Check the new instance name is valid.
		err = instancetype.ValidName(req.Name, false)
		if err != nil {
			return response.BadRequest(err)
		}

		// Check that the new isn't already in use.
		var id int
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Check that the name isn't already in use.
			id, _ = tx.GetInstanceID(ctx, instProject, req.Name)

			return nil
		})
		if id > 0 {
			return response.Conflict(fmt.Errorf("Instance name %q already in use", req.Name))
		}
	}

	// Load the local instance.
	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Handle simple instance renaming.
	if !req.Migration {
		run := func(*operations.Operation) error {
			return inst.Rename(req.Name, true)
		}

		resources := map[string][]api.URL{}
		resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}

		op, err := operations.OperationCreate(r.Context(), s, projectName, operations.OperationClassTask, operationtype.InstanceRename, resources, nil, run, nil, nil)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	// Start handling migrations.
	if inst.IsSnapshot() {
		return response.BadRequest(errors.New("Instance snapshots cannot be moved on their own"))
	}

	// Checks for running instances.
	if inst.IsRunning() && (req.Pool != "" || req.Project != "" || target != "") {
		// Stateless migrations require a stopped instance.
		if !req.Live {
			return response.BadRequest(errors.New("Instance must be stopped to be moved statelessly"))
		}

		// Storage pool changes require a stopped instance.
		if req.Pool != "" {
			return response.BadRequest(errors.New("Instance must be stopped to be moved across storage pools"))
		}

		// Project changes require a stopped instance.
		if req.Project != "" {
			return response.BadRequest(errors.New("Instance must be stopped to be moved across projects"))
		}

		// Name changes require a stopped instance.
		if req.Name != "" {
			return response.BadRequest(errors.New("Instance must be stopped to change their names"))
		}
	} else {
		// Clear Live flag if instance isn't running.
		req.Live = false
	}

	// Check for offline sources.
	if sourceMemberInfo != nil && sourceMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold()) && (req.Pool != "" || req.Project != "" || req.Name != "") {
		return response.BadRequest(errors.New("Instance server is currently offline"))
	}

	// When in a cluster, default to keeping current location.
	currentLocation := inst.Location()
	if instLocation == "" && currentLocation != "" {
		instLocation = currentLocation
	}

	// If clustered, consider a new location for the instance.
	var targetMemberInfo *db.NodeInfo
	var targetCandidates []db.NodeInfo
	if s.ServerClustered && (target != "" || req.Project != "") {
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			var targetGroupName string

			// Load the target project.
			p, err := dbCluster.GetProject(ctx, tx.Tx(), instProject)
			if err != nil {
				return err
			}

			targetProject, err := p.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			// Load the cluster members.
			allMembers, err := tx.GetNodes(ctx)
			if err != nil {
				return fmt.Errorf("Failed getting cluster members: %w", err)
			}

			// Check if the current location is fine.
			targetMemberInfo, _, err = limits.CheckTarget(ctx, s.Authorizer, tx, targetProject, instLocation, allMembers)
			if err == nil && targetMemberInfo != nil {
				return nil
			}

			// If we must change location, validate access to requested member/group (if provided).
			targetMemberInfo, targetGroupName, err = limits.CheckTarget(ctx, s.Authorizer, tx, targetProject, target, allMembers)
			if err != nil {
				return err
			}

			// If no specific server, get a list of allowed candidates.
			if targetMemberInfo == nil {
				clusterGroupsAllowed := limits.GetRestrictedClusterGroups(targetProject)

				targetCandidates, err = tx.GetCandidateMembers(ctx, allMembers, []int{inst.Architecture()}, targetGroupName, clusterGroupsAllowed, s.GlobalConfig.OfflineThreshold())
				if err != nil {
					return err
				}
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		// If no specific server and a placement scriplet exists, call it with the candidates.
		if targetMemberInfo == nil && s.GlobalConfig.InstancesPlacementScriptlet() != "" {
			leaderInfo, err := s.LeaderInfo()
			if err != nil {
				return response.InternalError(err)
			}

			req := apiScriptlet.InstancePlacement{
				InstancesPost: api.InstancesPost{
					Name: name,
					Type: api.InstanceType(instanceType.String()),
					InstancePut: api.InstancePut{
						Config:  inst.ExpandedConfig(),
						Devices: inst.ExpandedDevices().CloneNative(),
					},
				},
				Project: projectName,
				Reason:  apiScriptlet.InstancePlacementReasonRelocation,
			}

			targetMemberInfo, err = scriptlet.InstancePlacementRun(r.Context(), logger.Log, s, &req, targetCandidates, leaderInfo.Address)
			if err != nil {
				return response.BadRequest(fmt.Errorf("Failed instance placement scriptlet: %w", err))
			}
		}

		// If no member was selected yet, pick the member with the least number of instances.
		if targetMemberInfo == nil {
			var filteredCandidateMembers []db.NodeInfo

			// The instance might already be placed on the node with least number of instances.
			// Therefore remove it from the list of possible candidates if existent.
			for _, candidateMember := range targetCandidates {
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

		if targetMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold()) {
			return response.BadRequest(errors.New("Target cluster member is offline"))
		}
	}

	// Check that we're not requested to move to the same location we're currently on.
	if target != "" && targetMemberInfo.Name == inst.Location() {
		return response.BadRequest(errors.New("Requested target server is the same as current server"))
	}

	// If the instance needs to move, make sure it doesn't have backups.
	if targetMemberInfo != nil && targetMemberInfo.Name != inst.Location() {
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Check if instance has backups.
			backups, err := tx.GetInstanceBackups(ctx, projectName, name)
			if err != nil {
				return fmt.Errorf("Failed to fetch instance's backups: %w", err)
			}

			if len(backups) > 0 {
				return api.StatusErrorf(http.StatusBadRequest, "Instances with backups cannot be moved")
			}

			return err
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Server-side instance migration.
	if req.Pool != "" || req.Project != "" || target != "" {
		// Clear targetMemberInfo if no target change required.
		if targetMemberInfo != nil && inst.Location() == targetMemberInfo.Name {
			targetMemberInfo = nil
		}

		// Setup the instance move operation.
		run := func(op *operations.Operation) error {
			return instancePostMigration(s, inst, req, op)
		}

		resources := map[string][]api.URL{}
		resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}
		op, err := operations.OperationCreate(r.Context(), s, projectName, operations.OperationClassTask, operationtype.InstanceMigrate, resources, nil, run, nil, nil)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	// Cross-server instance migration.
	ws, err := newMigrationSource(inst, req.Live, req.InstanceOnly, req.AllowInconsistent, "", req.Target)
	if err != nil {
		return response.InternalError(err)
	}

	resources := map[string][]api.URL{}
	resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}
	run := func(op *operations.Operation) error {
		return ws.Do(s, op)
	}

	cancel := func(op *operations.Operation) error {
		ws.disconnect()
		return nil
	}

	if req.Target != nil {
		// Push mode.
		op, err := operations.OperationCreate(r.Context(), s, projectName, operations.OperationClassTask, operationtype.InstanceMigrate, resources, nil, run, nil, nil)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	// Pull mode.
	op, err := operations.OperationCreate(r.Context(), s, projectName, operations.OperationClassWebsocket, operationtype.InstanceMigrate, resources, ws.Metadata(), run, cancel, ws.Connect)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// Move an instance.
func instancePostMigration(s *state.State, inst instance.Instance, req api.InstancePost, op *operations.Operation) error {
	if inst.IsSnapshot() {
		return errors.New("Instance snapshots cannot be moved between pools")
	}

	if req.Project == "" {
		req.Project = inst.Project().Name
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
		req.Profiles = make([]string, 0, len(inst.Profiles()))
		for _, p := range inst.Profiles() {
			req.Profiles = append(req.Profiles, p.Name)
		}
	}

	apiProfiles := []api.Profile{}
	if len(req.Profiles) > 0 {
		err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
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

	// Specify the target instance config with the new name.
	args := db.InstanceArgs{
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

	// If we are moving the instance to a new pool but keeping the same instance name, then we need to create
	// the copy of the instance on the new pool with a temporary name that is different from the source to
	// avoid conflicts. Then after the source instance has been deleted we will rename the new instance back
	// to the original name.
	if req.Name == inst.Name() && req.Project == inst.Project().Name {
		args.Name, err = instance.MoveTemporaryName(inst)
		if err != nil {
			return err
		}
	}

	// Copy instance to new target instance.
	targetInst, err := instanceCreateAsCopy(s, instanceCreateAsCopyOpts{
		sourceInstance:           inst,
		targetInstance:           args,
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
		return fmt.Errorf("Failed to copy instance permissions: %w", err)
	}

	// Delete original instance.
	err = inst.Delete(true)
	if err != nil {
		return err
	}

	// Rename copy from temporary name to original name if needed.
	if req.Name == inst.Name() && req.Project == inst.Project().Name {
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
func instancePostClusteringMigrate(ctx context.Context, s *state.State, srcPool storagePools.Pool, srcInst instance.Instance, newInstName string, srcMember db.NodeInfo, newMember db.NodeInfo, stateful bool, allowInconsistent bool) (func(op *operations.Operation) error, error) {
	srcMemberOffline := srcMember.IsOffline(s.GlobalConfig.OfflineThreshold())

	// Make sure that the source member is online if we end up being called from another member after a
	// redirection due to the source member being offline.
	if srcMemberOffline {
		return nil, errors.New("The cluster member hosting the instance is offline")
	}

	// Save the original value of the "volatile.apply_template" config key,
	// since we'll want to preserve it in the copied instance.
	origVolatileApplyTemplate := srcInst.LocalConfig()["volatile.apply_template"]

	// Check we can convert the instance to the volume types needed.
	volType, err := storagePools.InstanceTypeToVolumeType(srcInst.Type())
	if err != nil {
		return nil, err
	}

	volDBType, err := storagePools.VolumeTypeToDBType(volType)
	if err != nil {
		return nil, err
	}

	run := func(op *operations.Operation) error {
		srcInstName := srcInst.Name()
		projectName := srcInst.Project().Name

		if newInstName == "" {
			newInstName = srcInstName
		}

		networkCert := s.Endpoints.NetworkCert()

		// Connect to the destination member, i.e. the member to migrate the instance to.
		// Use the notify argument to indicate to the destination that we are moving an instance between
		// cluster members.
		dest, err := cluster.Connect(ctx, newMember.Address, networkCert, s.ServerCert(), true)
		if err != nil {
			return fmt.Errorf("Failed to connect to destination server %q: %w", newMember.Address, err)
		}

		dest = dest.UseTarget(newMember.Name).UseProject(projectName)

		resources := map[string][]api.URL{}
		resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", srcInstName)}

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

			srcInst, err = instance.LoadByProjectAndName(s, projectName, newInstName)
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

		srcMigration, err := newMigrationSource(srcInst, live, false, allowInconsistent, srcInstName, nil)
		if err != nil {
			return fmt.Errorf("Failed setting up instance migration on source: %w", err)
		}

		run := func(op *operations.Operation) error {
			return srcMigration.Do(s, op)
		}

		cancel := func(op *operations.Operation) error {
			srcMigration.disconnect()
			return nil
		}

		srcOp, err := operations.OperationCreate(ctx, s, projectName, operations.OperationClassWebsocket, operationtype.InstanceMigrate, resources, srcMigration.Metadata(), run, cancel, srcMigration.Connect)
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
			err = tx.UpdateInstanceNode(ctx, projectName, srcInstName, newInstName, newMember.Name, srcPool.ID(), volDBType)
			if err != nil {
				return fmt.Errorf("Failed updating cluster member to %q for instance %q: %w", newMember.Name, newInstName, err)
			}

			// Restore the original value of "volatile.apply_template".
			id, err := dbCluster.GetInstanceID(ctx, tx.Tx(), projectName, newInstName)
			if err != nil {
				return fmt.Errorf("Failed to get ID of moved instance: %w", err)
			}

			err = tx.DeleteInstanceConfigKey(ctx, id, "volatile.apply_template")
			if err != nil {
				return fmt.Errorf("Failed to remove volatile.apply_template config key: %w", err)
			}

			if origVolatileApplyTemplate != "" {
				config := map[string]string{
					"volatile.apply_template": origVolatileApplyTemplate,
				}

				err = tx.CreateInstanceConfig(ctx, int(id), config)
				if err != nil {
					return fmt.Errorf("Failed to set volatile.apply_template config key: %w", err)
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
func instancePostClusteringMigrateWithRemoteStorage(s *state.State, srcPool storagePools.Pool, srcInst instance.Instance, newInstName string, newMember db.NodeInfo) (func(op *operations.Operation) error, error) {
	// Sense checks to avoid unexpected behaviour.
	if !srcPool.Driver().Info().Remote {
		return nil, errors.New("Source instance's storage pool is not remote shared storage")
	}

	// Check this function is only run on the target member.
	if s.ServerName != newMember.Name {
		return nil, errors.New("Remote shared storage instance move when source member is offline must be run on target member")
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

	run := func(op *operations.Operation) error {
		projectName := srcInst.Project().Name
		srcInstName := srcInst.Name()

		// Re-link the database entries against the new member name.
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			err := tx.UpdateInstanceNode(ctx, projectName, srcInstName, srcInstName, newMember.Name, srcPool.ID(), volDBType)
			if err != nil {
				return fmt.Errorf("Failed updating cluster member to %q for instance %q: %w", newMember.Name, srcInstName, err)
			}

			return nil
		})
		if err != nil {
			return fmt.Errorf("Failed to relink instance database data: %w", err)
		}

		if srcInstName != newInstName {
			err = srcInst.Rename(newInstName, true)
			if err != nil {
				return fmt.Errorf("Failed renaming instance %q to %q: %w", srcInstName, newInstName, err)
			}

			srcInst, err = instance.LoadByProjectAndName(s, projectName, newInstName)
			if err != nil {
				return fmt.Errorf("Failed loading renamed instance: %w", err)
			}

			srcInstName = srcInst.Name()
		}

		_, err = srcPool.ImportInstance(srcInst, nil, nil)
		if err != nil {
			return fmt.Errorf("Failed creating mount point of instance on target node: %w", err)
		}

		return nil
	}

	return run, nil
}

func migrateInstance(ctx context.Context, s *state.State, inst instance.Instance, targetNode string, req api.InstancePost, op *operations.Operation) error {
	// If target isn't the same as the instance's location.
	if targetNode == inst.Location() {
		return errors.New("Target must be different than instance's current location")
	}

	var err error
	var srcMember, newMember db.NodeInfo

	// If the source member is online then get its address so we can connect to it and see if the
	// instance is running later.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
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
		f, err := instancePostClusteringMigrateWithRemoteStorage(s, srcPool, inst, req.Name, newMember)
		if err != nil {
			return err
		}

		return f(op)
	}

	f, err := instancePostClusteringMigrate(ctx, s, srcPool, inst, req.Name, srcMember, newMember, req.Live, req.AllowInconsistent)
	if err != nil {
		return err
	}

	return f(op)
}
