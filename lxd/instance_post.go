package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	dbCluster "github.com/lxc/lxd/lxd/db/cluster"
	"github.com/lxc/lxd/lxd/db/operationtype"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/rbac"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

var internalClusterInstanceMovedCmd = APIEndpoint{
	Path: "cluster/instance-moved/{name}",

	Post: APIEndpointAction{Handler: internalClusterInstanceMovedPost},
}

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

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := projectParam(r)

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if shared.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	targetNode := queryParam(r, "target")

	// Flag indicating whether the node running the container is offline.
	sourceNodeOffline := false

	// Flag indicating whether the node the container should be moved to is
	// online (only relevant if "?target=<node>" was given).
	targetNodeOffline := false

	// A POST to /containers/<name>?target=<node> is meant to be used to
	// move a container from one node to another within a cluster.
	if targetNode != "" {
		// Determine if either the source node (the one currently
		// running the container) or the target node are offline.
		//
		// If the target node is offline, we return an error.
		//
		// If the source node is offline and the container is backed by
		// ceph, we'll just assume that the container is not running
		// and it's safe to move it.
		//
		// TODO: add some sort of "force" flag to the API, to signal
		//       that the user really wants to move the container even
		//       if we can't know for sure that it's indeed not
		//       running?
		err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			p, err := dbCluster.GetProject(ctx, tx.Tx(), projectName)
			if err != nil {
				return fmt.Errorf("Failed loading project: %w", err)
			}

			apiProject, err := p.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			// Check if user is allowed to use cluster member targeting
			err = project.CheckClusterTargetRestriction(r, apiProject, targetNode)
			if err != nil {
				return err
			}

			// Load target node.
			node, err := tx.GetNodeByName(ctx, targetNode)
			if err != nil {
				return fmt.Errorf("Failed to get target node: %w", err)
			}

			targetNodeOffline = node.IsOffline(s.GlobalConfig.OfflineThreshold())

			// Load source node.
			address, err := tx.GetNodeAddressOfInstance(ctx, projectName, name, instanceType)
			if err != nil {
				return fmt.Errorf("Failed to get address of instance's member: %w", err)
			}

			if address == "" {
				// Local node.
				sourceNodeOffline = false
				return nil
			}

			node, err = tx.GetNodeByAddress(ctx, address)
			if err != nil {
				return fmt.Errorf("Failed to get source member for %s: %w", address, err)
			}

			sourceNodeOffline = node.IsOffline(s.GlobalConfig.OfflineThreshold())

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}
	}

	if targetNode != "" && targetNodeOffline {
		return response.BadRequest(fmt.Errorf("Target node is offline"))
	}

	// Check whether to forward the request to the node that is running the
	// container. Here are the possible cases:
	//
	// 1. No "?target=<node>" parameter was passed. In this case this is
	//    just a container rename, with no move, and we want the request to be
	//    handled by the node which is actually running the container.
	//
	// 2. The "?target=<node>" parameter was set and the node running the
	//    container is online. In this case we want to forward the request to
	//    that node, which might do things like unmapping the RBD volume for
	//    ceph containers.
	//
	// 3. The "?target=<node>" parameter was set but the node running the
	//    container is offline. We don't want to forward to the request to
	//    that node and we don't want to load the container here (since
	//    it's not a local container): we'll be able to handle the request
	//    at all only if the container is backed by ceph. We'll check for
	//    that just below.
	//
	// Cases 1. and 2. are the ones for which the conditional will be true
	// and we'll either forward the request or load the container.
	if targetNode == "" || !sourceNodeOffline {
		// Handle requests targeted to a container on a different node.
		resp, err := forwardedResponseIfInstanceIsRemote(d, r, projectName, name, instanceType)
		if err != nil {
			return response.SmartError(err)
		}

		if resp != nil {
			return resp
		}
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

	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// If new instance name not supplied, assume it will be keeping its current name.
	if req.Name == "" {
		req.Name = inst.Name()
	}

	// Check the new instance name is valid.
	err = instance.ValidName(req.Name, false)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Migration {
		// Server-side pool migration.
		if req.Pool != "" {
			// Setup the instance move operation.
			run := func(op *operations.Operation) error {
				return instancePostPoolMigration(d, inst, req.Name, req.InstanceOnly, req.Pool, req.Live, req.AllowInconsistent, op)
			}

			resources := map[string][]string{}
			resources["instances"] = []string{name}
			op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.InstanceMigrate, resources, nil, run, nil, nil, r)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		// Server-side project migration.
		if req.Project != "" {
			// Check is user has access to target project
			if !rbac.UserHasPermission(r, req.Project, "manage-containers") {
				return response.Forbidden(nil)
			}

			// Setup the instance move operation.
			run := func(op *operations.Operation) error {
				return instancePostProjectMigration(d, inst, req.Name, req.Project, req.InstanceOnly, req.Live, req.AllowInconsistent, op)
			}

			resources := map[string][]string{}
			resources["instances"] = []string{name}
			op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.InstanceMigrate, resources, nil, run, nil, nil, r)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		if targetNode != "" {
			// Check if instance has backups.
			backups, err := d.db.Cluster.GetInstanceBackups(projectName, name)
			if err != nil {
				err = fmt.Errorf("Failed to fetch instance's backups: %w", err)
				return response.SmartError(err)
			}

			if len(backups) > 0 {
				return response.BadRequest(fmt.Errorf("Instance has backups"))
			}

			run := func(op *operations.Operation) error {
				return migrateInstance(d, r, inst, targetNode, sourceNodeOffline, req, op)
			}

			resources := map[string][]string{}
			resources["instances"] = []string{name}

			if inst.Type() == instancetype.Container {
				resources["containers"] = resources["instances"]
			}

			op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.InstanceMigrate, resources, nil, run, nil, nil, r)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		instanceOnly := req.InstanceOnly || req.ContainerOnly
		ws, err := newMigrationSource(inst, req.Live, instanceOnly, req.AllowInconsistent, false)
		if err != nil {
			return response.InternalError(err)
		}

		resources := map[string][]string{}
		resources["instances"] = []string{name}

		if inst.Type() == instancetype.Container {
			resources["containers"] = resources["instances"]
		}

		run := func(op *operations.Operation) error {
			return ws.Do(s, op)
		}

		cancel := func(op *operations.Operation) error {
			ws.disconnect()
			return nil
		}

		if req.Target != nil {
			// Push mode
			err := ws.ConnectContainerTarget(*req.Target)
			if err != nil {
				return response.InternalError(err)
			}

			op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.InstanceMigrate, resources, nil, run, nil, nil, r)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		// Pull mode.
		op, err := operations.OperationCreate(s, projectName, operations.OperationClassWebsocket, operationtype.InstanceMigrate, resources, ws.Metadata(), run, cancel, ws.Connect, r)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	// Check that the name isn't already in use.
	id, _ := d.db.Cluster.GetInstanceID(projectName, req.Name)
	if id > 0 {
		return response.Conflict(fmt.Errorf("Name %q already in use", req.Name))
	}

	run := func(*operations.Operation) error {
		return inst.Rename(req.Name, true)
	}

	resources := map[string][]string{}
	resources["instances"] = []string{name}

	if inst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.InstanceRename, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// Move an instance to another pool.
func instancePostPoolMigration(d *Daemon, inst instance.Instance, newName string, instanceOnly bool, newPool string, stateful bool, allowInconsistent bool, op *operations.Operation) error {
	if inst.IsSnapshot() {
		return fmt.Errorf("Instance snapshots cannot be moved between pools")
	}

	statefulStart := false
	if inst.IsRunning() {
		if stateful {
			statefulStart = true
			err := inst.Stop(true)
			if err != nil {
				return err
			}
		} else {
			return api.StatusErrorf(http.StatusBadRequest, "Instance must be stopped to move between pools statelessly")
		}
	}

	// Copy config from instance to avoid modifying it.
	localConfig := make(map[string]string)
	for k, v := range inst.LocalConfig() {
		localConfig[k] = v
	}

	// Load source root disk from expanded devices (in case instance doesn't have its own root disk).
	rootDevKey, rootDev, err := shared.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
	if err != nil {
		return err
	}

	// Copy device config from instance, and update target instance root disk device with the new pool name.
	localDevices := inst.LocalDevices().Clone()
	rootDev["pool"] = newPool
	localDevices[rootDevKey] = rootDev

	// Specify the target instance config with the new name and modified root disk config.
	args := db.InstanceArgs{
		Name:         newName,
		BaseImage:    localConfig["volatile.base_image"],
		Config:       localConfig,
		Devices:      localDevices,
		Project:      inst.Project().Name,
		Type:         inst.Type(),
		Architecture: inst.Architecture(),
		Description:  inst.Description(),
		Ephemeral:    inst.IsEphemeral(),
		Profiles:     inst.Profiles(),
		Stateful:     inst.IsStateful(),
	}

	// If we are moving the instance to a new pool but keeping the same instance name, then we need to create
	// the copy of the instance on the new pool with a temporary name that is different from the source to
	// avoid conflicts. Then after the source instance has been deleted we will rename the new instance back
	// to the original name.
	if newName == inst.Name() {
		args.Name, err = instance.MoveTemporaryName(inst)
		if err != nil {
			return err
		}
	}

	// Copy instance to new target instance.
	targetInst, err := instanceCreateAsCopy(d.State(), instanceCreateAsCopyOpts{
		sourceInstance:       inst,
		targetInstance:       args,
		instanceOnly:         instanceOnly,
		applyTemplateTrigger: false, // Don't apply templates when moving.
		allowInconsistent:    allowInconsistent,
	}, op)
	if err != nil {
		return err
	}

	// Delete original instance.
	err = inst.Delete(true)
	if err != nil {
		return err
	}

	// Rename copy from temporary name to original name if needed.
	if newName == inst.Name() {
		err = targetInst.Rename(newName, false) // Don't apply templates when moving.
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

// Move an instance to another project.
func instancePostProjectMigration(d *Daemon, inst instance.Instance, newName string, newProject string, instanceOnly bool, stateful bool, allowInconsistent bool, op *operations.Operation) error {
	localConfig := inst.LocalConfig()

	statefulStart := false
	if inst.IsRunning() {
		if stateful {
			statefulStart = true
			err := inst.Stop(true)
			if err != nil {
				return err
			}
		} else {
			return api.StatusErrorf(http.StatusBadRequest, "Instance must be stopped to move between projects statelessly")
		}
	}

	// Load source root disk from expanded devices (in case instance doesn't have its own root disk).
	rootDevKey, rootDev, err := shared.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
	if err != nil {
		return err
	}

	// Copy device config from instance
	localDevices := inst.LocalDevices().Clone()
	localDevices[rootDevKey] = rootDev

	// Specify the target instance config with the new name.
	args := db.InstanceArgs{
		Name:         newName,
		BaseImage:    localConfig["volatile.base_image"],
		Config:       localConfig,
		Devices:      localDevices,
		Project:      newProject,
		Type:         inst.Type(),
		Architecture: inst.Architecture(),
		Description:  inst.Description(),
		Ephemeral:    inst.IsEphemeral(),
		Profiles:     inst.Profiles(),
		Stateful:     inst.IsStateful(),
	}

	// Copy instance to new target instance.
	targetInst, err := instanceCreateAsCopy(d.State(), instanceCreateAsCopyOpts{
		sourceInstance:       inst,
		targetInstance:       args,
		instanceOnly:         instanceOnly,
		applyTemplateTrigger: false, // Don't apply templates when moving.
		allowInconsistent:    allowInconsistent,
	}, op)
	if err != nil {
		return err
	}

	// Delete original instance.
	err = inst.Delete(true)
	if err != nil {
		return err
	}

	if statefulStart {
		err = targetInst.Start(true)
		if err != nil {
			return err
		}
	}

	return nil
}

// Move a non-ceph container to another cluster node.
func instancePostClusteringMigrate(s *state.State, r *http.Request, srcInst instance.Instance, newInstName string, newMemberName string, stateful bool, allowInconsistent bool) (func(op *operations.Operation) error, error) {
	var sourceAddress string
	var targetAddress string

	// Save the original value of the "volatile.apply_template" config key,
	// since we'll want to preserve it in the copied container.
	origVolatileApplyTemplate := srcInst.LocalConfig()["volatile.apply_template"]

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		sourceAddress, err = tx.GetLocalNodeAddress(ctx)
		if err != nil {
			return fmt.Errorf("Failed to get local member address: %w", err)
		}

		newMember, err := tx.GetNodeByName(ctx, newMemberName)
		if err != nil {
			return fmt.Errorf("Failed to get new member address: %w", err)
		}

		targetAddress = newMember.Address

		return nil
	})
	if err != nil {
		return nil, err
	}

	sourcePool, err := storagePools.LoadByInstance(s, srcInst)
	if err != nil {
		return nil, fmt.Errorf("Failed loading instance storage pool: %w", err)
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
		srcInstName := srcInst.Name()
		projectName := srcInst.Project().Name

		if newInstName == "" {
			newInstName = srcInstName
		}

		networkCert := s.Endpoints.NetworkCert()

		// If we are retaining the same instance name whilst moving the instance between cluster members
		// then we need to perform some special case handling, so detect and enable that here.
		isClusterSameNameMove := newInstName == srcInstName

		// Connect to the destination member, i.e. the member to migrate the instance to.
		// Use the notify argument to indicate to the destination that we are moving an instance whilst
		// retaining its name.
		dest, err := cluster.Connect(targetAddress, networkCert, s.ServerCert(), r, isClusterSameNameMove)
		if err != nil {
			return fmt.Errorf("Failed to connect to destination server %q: %w", targetAddress, err)
		}

		dest = dest.UseTarget(newMemberName).UseProject(projectName)

		// Setup migration source.
		srcRenderRes, _, err := srcInst.Render()
		if err != nil {
			return fmt.Errorf("Failed getting source instance info: %w", err)
		}

		srcInstInfo, ok := srcRenderRes.(*api.Instance)
		if !ok {
			return fmt.Errorf("Unexpected result from source instance render: %w", err)
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

		srcMigration, err := newMigrationSource(srcInst, live, false, allowInconsistent, isClusterSameNameMove)
		if err != nil {
			return fmt.Errorf("Failed setting up instance migration on source: %w", err)
		}

		resources := map[string][]string{}
		resources["instances"] = []string{srcInstName}

		run := func(op *operations.Operation) error {
			return srcMigration.Do(s, op)
		}

		cancel := func(op *operations.Operation) error {
			srcMigration.disconnect()
			return nil
		}

		srcOp, err := operations.OperationCreate(s, projectName, operations.OperationClassWebsocket, operationtype.InstanceMigrate, resources, srcMigration.Metadata(), run, cancel, srcMigration.Connect, r)
		if err != nil {
			return err
		}

		err = srcOp.Start()
		if err != nil {
			return fmt.Errorf("Failed starting migration source operation: %w", err)
		}

		sourceSecrets := map[string]string{
			api.SecretNameControl:    srcMigration.controlSecret,
			api.SecretNameFilesystem: srcMigration.fsSecret,
		}

		if live {
			sourceSecrets[api.SecretNameState] = srcMigration.stateSecret
		}

		// Request pull mode migration on destination.
		destOp, err := dest.CreateInstance(api.InstancesPost{
			Name:        newInstName,
			InstancePut: srcInstInfo.Writable(),
			Type:        api.InstanceType(srcInstInfo.Type),
			Source: api.InstanceSource{
				Type:        "migration",
				Mode:        "pull",
				Operation:   fmt.Sprintf("https://%s%s", sourceAddress, srcOp.URL()),
				Websockets:  sourceSecrets,
				Certificate: string(networkCert.PublicKey()),
				Live:        live,
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

		err = destOp.Wait()
		if err != nil {
			return fmt.Errorf("Instance move to destination failed: %w", err)
		}

		_, err = srcOp.Wait(context.Background())
		if err != nil {
			return fmt.Errorf("Instance move to destination failed on source: %w", err)
		}

		// Delete the instance on source member.
		if isClusterSameNameMove {
			// If the instance is being moved as retaining its original name, then we cannot use the
			// normal delete process, and instead we need to send a special internal request to the
			// source member asking it to delete just the storage volume(s) for that instance.
			if sourcePool.Driver().Info().Remote {
				return api.StatusErrorf(http.StatusBadRequest, "Instance storage pool %q is not local", sourcePool.Name())
			}

			snapshots, err := srcInst.Snapshots()
			if err != nil {
				return fmt.Errorf("Failed getting instance snapshots: %w", err)
			}

			snapshotCount := len(snapshots)
			for k := range snapshots {
				// Delete the snapshots in reverse order.
				k = snapshotCount - 1 - k

				err = sourcePool.DeleteInstanceSnapshot(snapshots[k], nil)
				if err != nil {
					return fmt.Errorf("Failed delete instance snapshot %q on source member: %w", snapshots[k].Name(), err)
				}
			}

			err = sourcePool.DeleteInstance(srcInst, nil)
			if err != nil {
				return fmt.Errorf("Failed deleting instance on source member: %w", err)
			}
		} else {
			// If the instance is being renamed as part of the move then we can just use the normal
			// delete process to remove the instance from the source member.
			err := srcInst.Delete(true)
			if err != nil {
				return fmt.Errorf("Failed deleting instance %q: %w", srcInstName, err)
			}
		}

		err = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Update the instance's cluster member if the instance name hasn't changed.
			if isClusterSameNameMove {
				err = tx.UpdateInstanceNode(ctx, projectName, srcInstName, newInstName, newMemberName, sourcePool.ID(), volDBType)
				if err != nil {
					return fmt.Errorf("Failed updating cluster member to %q for instance %q: %w", newMemberName, newInstName, err)
				}
			}

			// Restore the original value of "volatile.apply_template".
			id, err := dbCluster.GetInstanceID(ctx, tx.Tx(), projectName, newInstName)
			if err != nil {
				return fmt.Errorf("Failed to get ID of moved instance: %w", err)
			}

			err = tx.DeleteInstanceConfigKey(id, "volatile.apply_template")
			if err != nil {
				return fmt.Errorf("Failed to remove volatile.apply_template config key: %w", err)
			}

			if origVolatileApplyTemplate != "" {
				config := map[string]string{
					"volatile.apply_template": origVolatileApplyTemplate,
				}

				err = tx.CreateInstanceConfig(int(id), config)
				if err != nil {
					return fmt.Errorf("Failed to set volatile.apply_template config key: %w", err)
				}
			}

			return nil
		})
		if err != nil {
			return err
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

// Special case migrating a container backed by ceph across two cluster nodes.
func instancePostClusteringMigrateWithCeph(d *Daemon, r *http.Request, inst instance.Instance, pool storagePools.Pool, newName string, sourceNodeOffline bool, newNode string, stateful bool) (func(op *operations.Operation) error, error) {
	if pool.Driver().Info().Name != "ceph" {
		return nil, fmt.Errorf("Source instance's storage pool is not of type ceph")
	}

	var err error
	var sourceMember db.NodeInfo

	if !sourceNodeOffline {
		// If the source member is online then get its address so we can connect to it and see if the
		// instance is running later.
		err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			sourceMember, err = tx.GetNodeByName(ctx, inst.Location())
			if err != nil {
				return fmt.Errorf("Failed getting cluster member of instance %q", inst.Name())
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	run := func(op *operations.Operation) error {
		// Stop instance if needed.
		startAgain := false
		if sourceMember.Address != "" {
			// Check if instance is running on source member, and if so, then try to stop it.
			source, err := cluster.Connect(sourceMember.Address, d.endpoints.NetworkCert(), d.serverCert(), r, true)
			if err != nil {
				return fmt.Errorf("Failed to connect to source server %q: %w", sourceMember.Address, err)
			}

			source = source.UseProject(inst.Project().Name)

			// Get instance state on source member.
			entry, _, err := source.GetInstance(inst.Name())
			if err != nil {
				return fmt.Errorf("Failed getting instance %q state: %w", inst.Name(), err)
			}

			if entry.StatusCode != api.Stopped {
				startAgain = true
				req := api.InstanceStatePut{
					Action:   "stop",
					Stateful: stateful,
					Timeout:  30,
				}

				op, err := source.UpdateInstanceState(inst.Name(), req, "")
				if err != nil {
					return err
				}

				err = op.Wait()
				if err != nil {
					return fmt.Errorf("Failed stopping instance %q: %w", inst.Name(), err)
				}
			}
		}

		// Check we can convert the instance to the volume types needed.
		volType, err := storagePools.InstanceTypeToVolumeType(inst.Type())
		if err != nil {
			return err
		}

		volDBType, err := storagePools.VolumeTypeToDBType(volType)
		if err != nil {
			return err
		}

		// Check source volume exists, and get its config.
		srcConfig, err := pool.GenerateInstanceBackupConfig(inst, false, op)
		if err != nil {
			return fmt.Errorf("Failed generating instance migration config: %w", err)
		}

		// Trigger a rename in the Ceph driver.
		args := migration.VolumeSourceArgs{
			Data: project.Instance(inst.Project().Name, newName), // Indicate new storage volume name.
			Info: &migration.Info{Config: srcConfig},
		}

		err = pool.MigrateInstance(inst, nil, &args, op)
		if err != nil {
			return fmt.Errorf("Failed to migrate ceph RBD volume: %w", err)
		}

		// Re-link the database entries against the new node name.
		err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			err := tx.UpdateInstanceNode(ctx, inst.Project().Name, inst.Name(), newName, newNode, pool.ID(), volDBType)
			if err != nil {
				return fmt.Errorf("Failed updating cluster member to %q for instance %q: %w", newName, inst.Name(), err)
			}

			return nil
		})
		if err != nil {
			return fmt.Errorf("Failed to relink instance database data: %w", err)
		}

		// Reload instance from database with new state now its been updated.
		inst, err := instance.LoadByProjectAndName(d.State(), inst.Project().Name, newName)
		if err != nil {
			return fmt.Errorf("Failed loading instance %q: %w", inst.Name(), err)
		}

		// Create the instance mount point on the target node.
		target, err := cluster.ConnectIfInstanceIsRemote(d.db.Cluster, inst.Project().Name, newName, d.endpoints.NetworkCert(), d.serverCert(), r, inst.Type())
		if err != nil {
			return fmt.Errorf("Failed to connect to target node: %w", err)
		}

		if target == nil {
			// Create the instance mount point.
			err := instancePostCreateInstanceMountPoint(d, inst)
			if err != nil {
				return fmt.Errorf("Failed creating mount point on target member: %w", err)
			}

			// Start the instance if needed.
			if startAgain {
				err = inst.Start(stateful)
				if err != nil {
					return fmt.Errorf("Failed starting instance %q: %w", inst.Name(), err)
				}
			}
		} else {
			// Create the instance mount point.
			req := internalClusterInstanceMovedPostRequest{
				Action: "create",
			}

			url := api.NewURL().Project(inst.Project().Name).Path("internal", "cluster", "instance-moved", newName)
			resp, _, err := target.RawQuery("POST", url.String(), req, "")
			if err != nil {
				return fmt.Errorf("Failed creating mount point on target member: %w", err)
			}

			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("Failed creating mount point on target member: %s", resp.Error)
			}

			// Start the instance if needed.
			if startAgain {
				req := api.InstanceStatePut{
					Action:   "start",
					Stateful: stateful,
				}

				op, err := target.UpdateInstanceState(inst.Name(), req, "")
				if err != nil {
					return err
				}

				err = op.Wait()
				if err != nil {
					return fmt.Errorf("Failed starting instance %q: %w", inst.Name(), err)
				}
			}
		}

		return nil
	}

	return run, nil
}

type internalClusterInstanceMovedPostRequest struct {
	Action string
}

// Notification that an instance was moved.
//
// At the moment it's used for ceph-based instances, where the target node needs
// to create the appropriate mount points.
func internalClusterInstanceMovedPost(d *Daemon, r *http.Request) response.Response {
	projectName := projectParam(r)
	instanceName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	req := internalClusterInstanceMovedPostRequest{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	s := d.State()
	inst, err := instance.LoadByProjectAndName(s, projectName, instanceName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading instance: %w", err))
	}

	if req.Action == "create" {
		err = instancePostCreateInstanceMountPoint(d, inst)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed creating instance mount point on target member: %w", err))
		}
	} else {
		return response.BadRequest(fmt.Errorf("Unrecognized action"))
	}

	return response.EmptySyncResponse
}

// Used after to create the appropriate mounts point after an instance has been moved.
func instancePostCreateInstanceMountPoint(d *Daemon, inst instance.Instance) error {
	pool, err := storagePools.LoadByInstance(d.State(), inst)
	if err != nil {
		return fmt.Errorf("Failed loading pool of instance on target node: %w", err)
	}

	err = pool.ImportInstance(inst, nil, nil)
	if err != nil {
		return fmt.Errorf("Failed creating mount point of instance on target node: %w", err)
	}

	return nil
}

func migrateInstance(d *Daemon, r *http.Request, inst instance.Instance, targetNode string, sourceNodeOffline bool, req api.InstancePost, op *operations.Operation) error {
	// If target isn't the same as the instance's location.
	if targetNode == inst.Location() {
		return fmt.Errorf("Target must be different than instance's current location")
	}

	// Check if we are migrating a ceph-based instance.
	pool, err := storagePools.LoadByInstance(d.State(), inst)
	if err != nil {
		return fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	if pool.Driver().Info().Name == "ceph" {
		f, err := instancePostClusteringMigrateWithCeph(d, r, inst, pool, req.Name, sourceNodeOffline, targetNode, req.Live)
		if err != nil {
			return err
		}

		return f(op)
	}

	// If this is not a ceph-based instance, make sure that the source node is online, and we didn't get here
	// only to handle the case where the instance is ceph-based.
	if sourceNodeOffline {
		err := fmt.Errorf("The cluster member hosting the instance is offline")
		return err
	}

	f, err := instancePostClusteringMigrate(d.State(), r, inst, req.Name, targetNode, req.Live, req.AllowInconsistent)
	if err != nil {
		return err
	}

	return f(op)
}
