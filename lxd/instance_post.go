package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/response"
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
// Rename or move/migrate an instance
//
// Renames, moves an instance between pools or migrates an instance to another server.
//
// The returned operation metadata will vary based on what's requested.
// For rename or move within the same server, this is a simple background operation with progress data.
// For migration, in the push case, this will similarly be a background
// operation with progress data, for the pull case, it will be a websocket
// operation with a number of secrets to be passed to the target server.
//
// ---
// consumes:
//   - application/json
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: body
//     name: migration
//     description: Migration request
//     schema:
//       $ref: "#/definitions/InstancePost"
// responses:
//   "202":
//     $ref: "#/responses/Operation"
//   "400":
//     $ref: "#/responses/BadRequest"
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"
func instancePost(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := projectParam(r)

	name := mux.Vars(r)["name"]
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
		err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
			// Load cluster configuration.
			config, err := cluster.ConfigLoad(tx)
			if err != nil {
				return errors.Wrap(err, "Failed to load LXD config")
			}

			// Check if user is allowed to use cluster member targeting
			err = project.CheckClusterTargetRestriction(tx, r, projectName, targetNode)
			if err != nil {
				return err
			}

			// Load target node.
			node, err := tx.GetNodeByName(targetNode)
			if err != nil {
				return errors.Wrap(err, "Failed to get target node")
			}
			targetNodeOffline = node.IsOffline(config.OfflineThreshold())

			// Load source node.
			address, err := tx.GetNodeAddressOfInstance(projectName, name, db.InstanceTypeFilter(instanceType))
			if err != nil {
				return errors.Wrap(err, "Failed to get address of instance's member")
			}
			if address == "" {
				// Local node.
				sourceNodeOffline = false
				return nil
			}
			node, err = tx.GetNodeByAddress(address)
			if err != nil {
				return errors.Wrapf(err, "Failed to get source member for %s", address)
			}
			sourceNodeOffline = node.IsOffline(config.OfflineThreshold())

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

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	rdr1 := ioutil.NopCloser(bytes.NewBuffer(body))
	rdr2 := ioutil.NopCloser(bytes.NewBuffer(body))

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

	inst, err := instance.LoadByProjectAndName(d.State(), projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	if req.Migration {
		// Server-side pool migration.
		if req.Pool != "" {
			// Setup the instance move operation.
			run := func(op *operations.Operation) error {
				return instancePostPoolMigration(d, inst, req.Name, req.InstanceOnly, req.Pool, req.Live, op)
			}

			resources := map[string][]string{}
			resources["instances"] = []string{name}
			op, err := operations.OperationCreate(d.State(), projectName, operations.OperationClassTask, db.OperationInstanceMigrate, resources, nil, run, nil, nil, r)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		if targetNode != "" {
			// Check if instance has backups.
			backups, err := d.cluster.GetInstanceBackups(projectName, name)
			if err != nil {
				err = errors.Wrap(err, "Failed to fetch instance's backups")
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

			op, err := operations.OperationCreate(d.State(), projectName, operations.OperationClassTask, db.OperationInstanceMigrate, resources, nil, run, nil, nil, r)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		instanceOnly := req.InstanceOnly || req.ContainerOnly
		ws, err := newMigrationSource(inst, req.Live, instanceOnly)
		if err != nil {
			return response.InternalError(err)
		}

		resources := map[string][]string{}
		resources["instances"] = []string{name}

		if inst.Type() == instancetype.Container {
			resources["containers"] = resources["instances"]
		}

		run := func(op *operations.Operation) error {
			return ws.Do(d.State(), op)
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

			op, err := operations.OperationCreate(d.State(), projectName, operations.OperationClassTask, db.OperationInstanceMigrate, resources, nil, run, nil, nil, r)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		// Pull mode.
		op, err := operations.OperationCreate(d.State(), projectName, operations.OperationClassWebsocket, db.OperationInstanceMigrate, resources, ws.Metadata(), run, cancel, ws.Connect, r)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	// Check that the name isn't already in use.
	id, _ := d.cluster.GetInstanceID(projectName, req.Name)
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

	op, err := operations.OperationCreate(d.State(), projectName, operations.OperationClassTask, db.OperationInstanceRename, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// Move an instance to another pool.
func instancePostPoolMigration(d *Daemon, inst instance.Instance, newName string, instanceOnly bool, newPool string, stateful bool, op *operations.Operation) error {
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
		Project:      inst.Project(),
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
		args.Name = instance.MoveTemporaryName(inst)
	}

	// Copy instance to new target instance.
	targetInst, err := instanceCreateAsCopy(d.State(), instanceCreateAsCopyOpts{
		sourceInstance:       inst,
		targetInstance:       args,
		instanceOnly:         instanceOnly,
		applyTemplateTrigger: false, // Don't apply templates when moving.
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

// Move a non-ceph container to another cluster node.
func instancePostClusteringMigrate(d *Daemon, r *http.Request, inst instance.Instance, newName string, newNode string, stateful bool) (func(op *operations.Operation) error, error) {
	var sourceAddress string
	var targetAddress string

	// Save the original value of the "volatile.apply_template" config key,
	// since we'll want to preserve it in the copied container.
	origVolatileApplyTemplate := inst.LocalConfig()["volatile.apply_template"]

	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		sourceAddress, err = tx.GetLocalNodeAddress()
		if err != nil {
			return errors.Wrap(err, "Failed to get local node address")
		}

		node, err := tx.GetNodeByName(newNode)
		if err != nil {
			return errors.Wrap(err, "Failed to get new node address")
		}
		targetAddress = node.Address

		return nil
	})
	if err != nil {
		return nil, err
	}

	run := func(op *operations.Operation) error {
		// Connect to the source host, i.e. ourselves (the node the instance is running on).
		source, err := cluster.Connect(sourceAddress, d.endpoints.NetworkCert(), d.serverCert(), r, true)
		if err != nil {
			return fmt.Errorf("Failed to connect to source server %q: %w", sourceAddress, err)
		}
		source = source.UseProject(inst.Project())

		// Connect to the destination host, i.e. the node to migrate the container to.
		dest, err := cluster.Connect(targetAddress, d.endpoints.NetworkCert(), d.serverCert(), r, false)
		if err != nil {
			return fmt.Errorf("Failed to connect to destination server %q: %w", targetAddress, err)
		}
		dest = dest.UseTarget(newNode).UseProject(inst.Project())

		destName := newName
		isSameName := false

		// If no new name was provided, the user wants to keep the same
		// container name. In that case we need to generate a temporary
		// name.
		if destName == "" || destName == inst.Name() {
			isSameName = true
			destName = instance.MoveTemporaryName(inst)
		}

		// First make a copy on the new node of the container to be moved.
		entry, _, err := source.GetInstance(inst.Name())
		if err != nil {
			return fmt.Errorf("Failed getting instance %q state: %w", inst.Name(), err)
		}

		startAgain := false
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

			// Copy the stateful indicator to the new instance so that when it is started
			// again it will use the state file when doing a stateful start.
			entry.Stateful = stateful
		}

		args := lxd.InstanceCopyArgs{
			Name: destName,
			Mode: "pull",
		}

		copyOp, err := dest.CopyInstance(source, *entry, &args)
		if err != nil {
			return errors.Wrap(err, "Failed to issue copy instance API request")
		}

		handler := func(newOp api.Operation) {
			op.UpdateMetadata(newOp.Metadata)
		}

		_, err = copyOp.AddHandler(handler)
		if err != nil {
			return err
		}

		err = copyOp.Wait()
		if err != nil {
			return errors.Wrap(err, "Copy instance operation failed")
		}

		// Delete the container on the original node.
		deleteOp, err := source.DeleteInstance(inst.Name())
		if err != nil {
			return errors.Wrap(err, "Failed to issue delete instance API request")
		}

		err = deleteOp.Wait()
		if err != nil {
			return errors.Wrap(err, "Delete instance operation failed")
		}

		// If the destination name is not set, we have generated a random name for
		// the new container, so we need to rename it.
		if isSameName {
			instancePost := api.InstancePost{
				Name: inst.Name(),
			}

			op, err := dest.RenameInstance(destName, instancePost)
			if err != nil {
				return errors.Wrap(err, "Failed to issue rename instance API request")
			}

			err = op.Wait()
			if err != nil {
				return errors.Wrap(err, "Rename instance operation failed")
			}
			destName = inst.Name()
		}

		// Restore the original value of "volatile.apply_template"
		project := inst.Project()
		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			id, err := tx.GetInstanceID(project, destName)
			if err != nil {
				return errors.Wrap(err, "Failed to get ID of moved instance")
			}
			err = tx.DeleteInstanceConfigKey(id, "volatile.apply_template")
			if err != nil {
				return errors.Wrap(err, "Failed to remove volatile.apply_template config key")
			}

			if origVolatileApplyTemplate != "" {
				config := map[string]string{
					"volatile.apply_template": origVolatileApplyTemplate,
				}
				err = tx.CreateInstanceConfig(int(id), config)
				if err != nil {
					return errors.Wrap(err, "Failed to set volatile.apply_template config key")
				}
			}

			return nil
		})
		if err != nil {
			return err
		}

		if startAgain {
			req := api.InstanceStatePut{
				Action:   "start",
				Stateful: stateful,
			}

			op, err := dest.UpdateInstanceState(destName, req, "")
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return fmt.Errorf("Failed starting instance %q: %w", inst.Name(), err)
			}
		}

		return nil
	}

	return run, nil
}

// Special case migrating a container backed response.Responseby ceph across two cluster nodes.
func instancePostClusteringMigrateWithCeph(d *Daemon, r *http.Request, inst instance.Instance, pool storagePools.Pool, newName string, sourceNodeOffline bool, newNode string, stateful bool) (func(op *operations.Operation) error, error) {
	if pool.Driver().Info().Name != "ceph" {
		return nil, fmt.Errorf("Source instance's storage pool is not of type ceph")
	}

	var err error
	var sourceMember db.NodeInfo

	if !sourceNodeOffline {
		// If the source member is online then get its address so we can connect to it and see if the
		// instance is running later.
		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			sourceMember, err = tx.GetNodeByName(inst.Location())
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
			source = source.UseProject(inst.Project())

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

		// Trigger a rename in the Ceph driver.
		args := migration.VolumeSourceArgs{
			Data: project.Instance(inst.Project(), newName), // Indicate new storage volume name.
		}
		err = pool.MigrateInstance(inst, nil, &args, op)
		if err != nil {
			return errors.Wrap(err, "Failed to migrate ceph RBD volume")
		}

		// Re-link the database entries against the new node name.
		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			err := tx.UpdateInstanceNode(inst.Project(), inst.Name(), newName, newNode, volDBType)
			if err != nil {
				return fmt.Errorf("Failed updating cluster member to %q for instance %q: %w", newName, inst.Name(), err)
			}

			return nil
		})
		if err != nil {
			return fmt.Errorf("Failed to relink instance database data: %w", err)
		}

		// Reload instance from database with new state now its been updated.
		inst, err := instance.LoadByProjectAndName(d.State(), inst.Project(), newName)
		if err != nil {
			return fmt.Errorf("Failed loading instance %q: %w", inst.Name(), err)
		}

		// Create the instance mount point on the target node.
		target, err := cluster.ConnectIfInstanceIsRemote(d.cluster, inst.Project(), newName, d.endpoints.NetworkCert(), d.serverCert(), r, inst.Type())
		if err != nil {
			return errors.Wrap(err, "Failed to connect to target node")
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
			// Use the correct project.
			target = target.UseProject(inst.Project())

			// Create the instance mount point.
			url := api.NewURL().Project(inst.Project()).Path("internal", "cluster", "instance-moved", newName)
			resp, _, err := target.RawQuery("POST", url.String(), nil, "")
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

// Notification that an instance was moved.
//
// At the moment it's used for ceph-based instances, where the target node needs
// to create the appropriate mount points.
func internalClusterInstanceMovedPost(d *Daemon, r *http.Request) response.Response {
	projectName := projectParam(r)
	instanceName := mux.Vars(r)["name"]

	inst, err := instance.LoadByProjectAndName(d.State(), projectName, instanceName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading instance on target node: %w", err))
	}

	err = instancePostCreateInstanceMountPoint(d, inst)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// Used after to create the appropriate mounts point after an instance has been moved.
func instancePostCreateInstanceMountPoint(d *Daemon, inst instance.Instance) error {
	pool, err := storagePools.GetPoolByInstance(d.State(), inst)
	if err != nil {
		return errors.Wrap(err, "Failed loading pool of instance on target node")
	}

	err = pool.ImportInstance(inst, nil)
	if err != nil {
		return errors.Wrap(err, "Failed creating mount point of instance on target node")
	}

	return nil
}

func migrateInstance(d *Daemon, r *http.Request, inst instance.Instance, targetNode string, sourceNodeOffline bool, req api.InstancePost, op *operations.Operation) error {
	// If target isn't the same as the instance's location.
	if targetNode == inst.Location() {
		return fmt.Errorf("Target must be different than instance's current location")
	}

	// Check if we are migrating a ceph-based instance.
	pool, err := storagePools.GetPoolByInstance(d.State(), inst)
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

	f, err := instancePostClusteringMigrate(d, r, inst, req.Name, targetNode, req.Live)
	if err != nil {
		return err
	}

	return f(op)
}
