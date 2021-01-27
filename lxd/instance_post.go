package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"

	lxd "github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/response"
	driver "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

var internalClusterContainerMovedCmd = APIEndpoint{
	Path: "cluster/container-moved/{name}",

	Post: APIEndpointAction{Handler: internalClusterContainerMovedPost},
}

func instancePost(d *Daemon, r *http.Request) response.Response {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	project := projectParam(r)

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

			// Load target node.
			node, err := tx.GetNodeByName(targetNode)
			if err != nil {
				return errors.Wrap(err, "Failed to get target node")
			}
			targetNodeOffline = node.IsOffline(config.OfflineThreshold())

			// Load source node.
			address, err := tx.GetNodeAddressOfInstance(project, name, instanceType)
			if err != nil {
				return errors.Wrap(err, "Failed to get address of instance's node")
			}
			if address == "" {
				// Local node.
				sourceNodeOffline = false
				return nil
			}
			node, err = tx.GetNodeByAddress(address)
			if err != nil {
				return errors.Wrapf(err, "Failed to get source node for %s", address)
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
		resp, err := forwardedResponseIfInstanceIsRemote(d, r, project, name, instanceType)
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

	// Check if stateful (backward compatibility).
	stateful := true
	_, err = reqRaw.GetBool("live")
	if err == nil {
		stateful = req.Live
	}

	inst, err := instance.LoadByProjectAndName(d.State(), project, name)
	if err != nil {
		return response.SmartError(err)
	}

	if req.Migration {
		// Server-side pool migration.
		if req.Pool != "" {
			// Setup the instance move operation.
			run := func(op *operations.Operation) error {
				return instancePostPoolMigration(d, inst, req.Name, req.InstanceOnly, req.Pool, op)
			}

			resources := map[string][]string{}
			resources["instances"] = []string{name}
			op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, db.OperationInstanceMigrate, resources, nil, run, nil, nil)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		if targetNode != "" {
			// Check if instance has backups.
			backups, err := d.cluster.GetInstanceBackups(project, name)
			if err != nil {
				err = errors.Wrap(err, "Failed to fetch instance's backups")
				return response.SmartError(err)
			}
			if len(backups) > 0 {
				return response.BadRequest(fmt.Errorf("Instance has backups"))
			}

			// Check whether the instance is running.
			if !sourceNodeOffline && inst.IsRunning() {
				return response.BadRequest(fmt.Errorf("Container is running"))
			}

			// Check if we are migrating a ceph-based container.
			poolName, err := d.cluster.GetInstancePool(project, name)
			if err != nil {
				err = errors.Wrap(err, "Failed to fetch instance's pool name")
				return response.SmartError(err)
			}
			_, pool, _, err := d.cluster.GetStoragePool(poolName)
			if err != nil {
				err = errors.Wrap(err, "Failed to fetch instance's pool info")
				return response.SmartError(err)
			}
			if pool.Driver == "ceph" {
				return instancePostClusteringMigrateWithCeph(d, inst, project, name, req.Name, targetNode, instanceType)
			}

			// If this is not a ceph-based container, make sure
			// that the source node is online, and we didn't get
			// here only to handle the case where the container is
			// ceph-based.
			if sourceNodeOffline {
				err := fmt.Errorf("The cluster member hosting the instance is offline")
				return response.SmartError(err)
			}

			return instancePostClusteringMigrate(d, inst, name, req.Name, targetNode)
		}

		instanceOnly := req.InstanceOnly || req.ContainerOnly
		ws, err := newMigrationSource(inst, stateful, instanceOnly)
		if err != nil {
			return response.InternalError(err)
		}

		resources := map[string][]string{}
		resources["instances"] = []string{name}
		resources["containers"] = resources["instances"]

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

			op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, db.OperationInstanceMigrate, resources, nil, run, nil, nil)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		// Pull mode.
		op, err := operations.OperationCreate(d.State(), project, operations.OperationClassWebsocket, db.OperationInstanceMigrate, resources, ws.Metadata(), run, cancel, ws.Connect)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	// Check that the name isn't already in use.
	id, _ := d.cluster.GetInstanceID(project, req.Name)
	if id > 0 {
		return response.Conflict(fmt.Errorf("Name '%s' already in use", req.Name))
	}

	run := func(*operations.Operation) error {
		return inst.Rename(req.Name, true)
	}

	resources := map[string][]string{}
	resources["instances"] = []string{name}
	resources["containers"] = resources["instances"]

	op, err := operations.OperationCreate(d.State(), project, operations.OperationClassTask, db.OperationInstanceRename, resources, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// Move an instance to another pool.
func instancePostPoolMigration(d *Daemon, inst instance.Instance, newName string, instanceOnly bool, newPool string, op *operations.Operation) error {
	if inst.IsRunning() {
		return fmt.Errorf("Instance must not be running to move between pools")
	}

	if inst.IsSnapshot() {
		return fmt.Errorf("Instance snapshots cannot be moved between pools")
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

	// If we are moving the instance to a new name, then we need to create the copy of the instance on the new
	// pool with a temporary name that is different from the source to avoid conflicts. Then after the source
	// instance has been deleted we will rename the new instance back to the original name.
	if newName == inst.Name() {
		args.Name = fmt.Sprintf("lxd-copy-of-%d", inst.ID())
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

	return nil
}

// Move a non-ceph container to another cluster node.
func instancePostClusteringMigrate(d *Daemon, c instance.Instance, oldName, newName, newNode string) response.Response {
	cert := d.endpoints.NetworkCert()

	var sourceAddress string
	var targetAddress string

	// Save the original value of the "volatile.apply_template" config key,
	// since we'll want to preserve it in the copied container.
	origVolatileApplyTemplate := c.LocalConfig()["volatile.apply_template"]

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
		return response.SmartError(err)
	}

	run := func(*operations.Operation) error {
		// Connect to the source host, i.e. ourselves (the node the instance is running on).
		source, err := cluster.Connect(sourceAddress, cert, true)
		if err != nil {
			return errors.Wrap(err, "Failed to connect to source server")
		}

		// Connect to the destination host, i.e. the node to migrate the container to.
		dest, err := cluster.Connect(targetAddress, cert, false)
		if err != nil {
			return errors.Wrap(err, "Failed to connect to destination server")
		}
		dest = dest.UseTarget(newNode)

		destName := newName
		isSameName := false

		// If no new name was provided, the user wants to keep the same
		// container name. In that case we need to generate a temporary
		// name.
		if destName == "" || destName == oldName {
			isSameName = true
			destName = fmt.Sprintf("move-%s", uuid.NewRandom().String())
		}

		// First make a copy on the new node of the container to be moved.
		entry, _, err := source.GetInstance(oldName)
		if err != nil {
			return errors.Wrap(err, "Failed to get instance info")
		}

		args := lxd.InstanceCopyArgs{
			Name: destName,
			Mode: "pull",
		}

		copyOp, err := dest.CopyInstance(source, *entry, &args)
		if err != nil {
			return errors.Wrap(err, "Failed to issue copy instance API request")
		}

		err = copyOp.Wait()
		if err != nil {
			return errors.Wrap(err, "Copy instance operation failed")
		}

		// Delete the container on the original node.
		deleteOp, err := source.DeleteInstance(oldName)
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
				Name: oldName,
			}

			op, err := dest.RenameInstance(destName, instancePost)
			if err != nil {
				return errors.Wrap(err, "Failed to issue rename instance API request")
			}

			err = op.Wait()
			if err != nil {
				return errors.Wrap(err, "Rename instance operation failed")
			}
			destName = oldName
		}

		// Restore the original value of "volatile.apply_template"
		project := c.Project()
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

		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{oldName}
	op, err := operations.OperationCreate(d.State(), c.Project(), operations.OperationClassTask, db.OperationInstanceMigrate, resources, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// Special case migrating a container backed by ceph across two cluster nodes.
func instancePostClusteringMigrateWithCeph(d *Daemon, c instance.Instance, projectName, oldName, newName, newNode string, instanceType instancetype.Type) response.Response {
	run := func(op *operations.Operation) error {
		// If source node is online (i.e. we're serving the request on
		// it, and c != nil), let's unmap the RBD volume locally
		logger.Debugf(`Renaming RBD storage volume for source container "%s" from "%s" to "%s"`, c.Name(), c.Name(), newName)
		poolName, err := c.StoragePool()
		if err != nil {
			return errors.Wrap(err, "Failed to get source instance's storage pool name")
		}

		pool, err := driver.GetPoolByName(d.State(), poolName)
		if err != nil {
			return errors.Wrap(err, "Failed to get source instance's storage pool")
		}

		if pool.Driver().Info().Name != "ceph" {
			return fmt.Errorf("Source instance's storage pool is not of type ceph")
		}

		args := migration.VolumeSourceArgs{
			Data: project.Instance(projectName, newName),
		}

		// Trigger a rename in the Ceph driver.
		err = pool.MigrateInstance(c, nil, &args, op)
		if err != nil {
			return errors.Wrap(err, "Failed to rename ceph RBD volume")
		}

		// Re-link the database entries against the new node name.
		err = d.cluster.Transaction(func(tx *db.ClusterTx) error {
			err := tx.UpdateInstanceNode(projectName, oldName, newName, newNode)
			if err != nil {
				return errors.Wrapf(
					err, "Move container %s to %s with new name %s", oldName, newNode, newName)
			}

			return nil
		})
		if err != nil {
			return errors.Wrap(err, "Failed to relink instance database data")
		}

		// Create the container mount point on the target node
		cert := d.endpoints.NetworkCert()
		client, err := cluster.ConnectIfInstanceIsRemote(d.cluster, projectName, newName, cert, instanceType)
		if err != nil {
			return errors.Wrap(err, "Failed to connect to target node")
		}
		if client == nil {
			err := instancePostCreateContainerMountPoint(d, projectName, newName)
			if err != nil {
				return errors.Wrap(err, "Failed to create mount point on target node")
			}
		} else {
			path := fmt.Sprintf("/internal/cluster/container-moved/%s?project=%s", newName, projectName)
			resp, _, err := client.RawQuery("POST", path, nil, "")
			if err != nil {
				return errors.Wrap(err, "Failed to create mount point on target node")
			}
			if resp.StatusCode != 200 {
				return fmt.Errorf("Failed to create mount point on target node: %s", resp.Error)
			}
		}

		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{oldName}
	op, err := operations.OperationCreate(d.State(), projectName, operations.OperationClassTask, db.OperationInstanceMigrate, resources, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// Notification that a container was moved.
//
// At the moment it's used for ceph-based containers, where the target node needs
// to create the appropriate mount points.
func internalClusterContainerMovedPost(d *Daemon, r *http.Request) response.Response {
	project := projectParam(r)
	containerName := mux.Vars(r)["name"]
	err := instancePostCreateContainerMountPoint(d, project, containerName)
	if err != nil {
		return response.SmartError(err)
	}
	return response.EmptySyncResponse
}

// Used after to create the appropriate mounts point after a container has been
// moved.
func instancePostCreateContainerMountPoint(d *Daemon, project, containerName string) error {
	c, err := instance.LoadByProjectAndName(d.State(), project, containerName)
	if err != nil {
		return errors.Wrap(err, "Failed to load moved instance on target node")
	}
	poolName, err := c.StoragePool()
	if err != nil {
		return errors.Wrap(err, "Failed get pool name of moved instance on target node")
	}
	snapshotNames, err := d.cluster.GetInstanceSnapshotsNames(project, containerName)
	if err != nil {
		return errors.Wrap(err, "Failed to create instance snapshot names")
	}

	containerMntPoint := driver.GetContainerMountPoint(c.Project(), poolName, containerName)
	err = driver.CreateContainerMountpoint(containerMntPoint, c.Path(), c.IsPrivileged())
	if err != nil {
		return errors.Wrap(err, "Failed to create instance mount point on target node")
	}

	for _, snapshotName := range snapshotNames {
		mntPoint := driver.GetSnapshotMountPoint(project, poolName, snapshotName)
		snapshotsSymlinkTarget := shared.VarPath("storage-pools",
			poolName, "containers-snapshots", containerName)
		snapshotMntPointSymlink := shared.VarPath("snapshots", containerName)
		err := driver.CreateSnapshotMountpoint(mntPoint, snapshotsSymlinkTarget, snapshotMntPointSymlink)
		if err != nil {
			return errors.Wrap(err, "Failed to create snapshot mount point on target node")
		}
	}

	return nil
}
