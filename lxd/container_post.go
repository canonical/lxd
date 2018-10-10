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
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

var internalClusterContainerMovedCmd = Command{
	name: "cluster/container-moved/{name}",
	post: internalClusterContainerMovedPost,
}

func containerPost(d *Daemon, r *http.Request) Response {
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
			node, err := tx.NodeByName(targetNode)
			if err != nil {
				return errors.Wrap(err, "Failed to get target node")
			}
			targetNodeOffline = node.IsOffline(config.OfflineThreshold())

			// Load source node.
			address, err := tx.ContainerNodeAddress(project, name)
			if err != nil {
				return errors.Wrap(err, "Failed to get address of container's node")
			}
			if address == "" {
				// Local node
				sourceNodeOffline = false
				return nil
			}
			node, err = tx.NodeByAddress(address)
			if err != nil {
				return errors.Wrapf(err, "Failed to get source node for %s", address)
			}
			sourceNodeOffline = node.IsOffline(config.OfflineThreshold())

			return nil
		})
		if err != nil {
			return SmartError(err)
		}
	}

	if targetNode != "" && targetNodeOffline {
		return BadRequest(fmt.Errorf("Target node is offline"))
	}

	var c container

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
		// Handle requests targeted to a container on a different node
		response, err := ForwardedResponseIfContainerIsRemote(d, r, project, name)
		if err != nil {
			return SmartError(err)
		}
		if response != nil {
			return response
		}

		c, err = containerLoadByProjectAndName(d.State(), project, name)
		if err != nil {
			return SmartError(err)
		}
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return InternalError(err)
	}

	rdr1 := ioutil.NopCloser(bytes.NewBuffer(body))
	rdr2 := ioutil.NopCloser(bytes.NewBuffer(body))

	reqRaw := shared.Jmap{}
	err = json.NewDecoder(rdr1).Decode(&reqRaw)
	if err != nil {
		return BadRequest(err)
	}

	req := api.ContainerPost{}
	err = json.NewDecoder(rdr2).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Check if stateful (backward compatibility)
	stateful := true
	_, err = reqRaw.GetBool("live")
	if err == nil {
		stateful = req.Live
	}

	if req.Migration {
		if targetNode != "" {
			// Check whether the container is running.
			if c != nil && c.IsRunning() {
				return BadRequest(fmt.Errorf("Container is running"))
			}

			// Check if we are migrating a ceph-based container.
			poolName, err := d.cluster.ContainerPool(project, name)
			if err != nil {
				err = errors.Wrap(err, "Failed to fetch container's pool name")
				return SmartError(err)
			}
			_, pool, err := d.cluster.StoragePoolGet(poolName)
			if err != nil {
				err = errors.Wrap(err, "Failed to fetch container's pool info")
				return SmartError(err)
			}
			if pool.Driver == "ceph" {
				return containerPostClusteringMigrateWithCeph(d, c, project, name, req.Name, targetNode)
			}

			// If this is not a ceph-based container, make sure
			// that the source node is online, and we didn't get
			// here only to handle the case where the container is
			// ceph-based.
			if sourceNodeOffline {
				err := fmt.Errorf("The cluster member hosting the container is offline")
				return SmartError(err)
			}

			return containerPostClusteringMigrate(d, c, name, req.Name, targetNode)
		}

		ws, err := NewMigrationSource(c, stateful, req.ContainerOnly)
		if err != nil {
			return InternalError(err)
		}

		resources := map[string][]string{}
		resources["containers"] = []string{name}

		if req.Target != nil {
			// Push mode
			err := ws.ConnectContainerTarget(*req.Target)
			if err != nil {
				return InternalError(err)
			}

			op, err := operationCreate(d.cluster, project, operationClassTask, db.OperationContainerMigrate, resources, nil, ws.Do, nil, nil)
			if err != nil {
				return InternalError(err)
			}

			return OperationResponse(op)
		}

		// Pull mode
		op, err := operationCreate(d.cluster, project, operationClassWebsocket, db.OperationContainerMigrate, resources, ws.Metadata(), ws.Do, nil, ws.Connect)
		if err != nil {
			return InternalError(err)
		}

		return OperationResponse(op)
	}

	// Check that the name isn't already in use
	id, _ := d.cluster.ContainerID(req.Name)
	if id > 0 {
		return Conflict(fmt.Errorf("Name '%s' already in use", req.Name))
	}

	run := func(*operation) error {
		return c.Rename(req.Name)
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operationCreate(d.cluster, project, operationClassTask, db.OperationContainerRename, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

// Move a non-ceph container to another cluster node.
func containerPostClusteringMigrate(d *Daemon, c container, oldName, newName, newNode string) Response {
	cert := d.endpoints.NetworkCert()

	var sourceAddress string
	var targetAddress string

	// Save the original value of the "volatile.apply_template" config key,
	// since we'll want to preserve it in the copied container.
	origVolatileApplyTemplate := c.LocalConfig()["volatile.apply_template"]

	err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
		var err error

		sourceAddress, err = tx.NodeAddress()
		if err != nil {
			return errors.Wrap(err, "Failed to get local node address")
		}

		node, err := tx.NodeByName(newNode)
		if err != nil {
			return errors.Wrap(err, "Failed to get new node address")
		}
		targetAddress = node.Address

		return nil
	})
	if err != nil {
		return SmartError(err)
	}

	run := func(*operation) error {
		// Connect to the source host, i.e. ourselves (the node the container is running on).
		source, err := cluster.Connect(sourceAddress, cert, false)
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
		entry, _, err := source.GetContainer(oldName)
		if err != nil {
			return errors.Wrap(err, "Failed to get container info")
		}

		args := lxd.ContainerCopyArgs{
			Name: destName,
			Mode: "pull",
		}

		copyOp, err := dest.CopyContainer(source, *entry, &args)
		if err != nil {
			return errors.Wrap(err, "Failed to issue copy container API request")
		}

		err = copyOp.Wait()
		if err != nil {
			return errors.Wrap(err, "Copy container operation failed")
		}

		// Delete the container on the original node.
		deleteOp, err := source.DeleteContainer(oldName)
		if err != nil {
			return errors.Wrap(err, "Failed to issue delete container API request")
		}

		err = deleteOp.Wait()
		if err != nil {
			return errors.Wrap(err, "Delete container operation failed")
		}

		// If the destination name is not set, we have generated a random name for
		// the new container, so we need to rename it.
		if isSameName {
			containerPost := api.ContainerPost{
				Name: oldName,
			}

			op, err := dest.RenameContainer(destName, containerPost)
			if err != nil {
				return errors.Wrap(err, "Failed to issue rename container API request")
			}

			err = op.Wait()
			if err != nil {
				return errors.Wrap(err, "Rename container operation failed")
			}
			destName = oldName
		}

		// Restore the original value of "volatile.apply_template"
		id, err := d.cluster.ContainerID(destName)
		if err != nil {
			return errors.Wrap(err, "Failed to get ID of moved container")
		}

		err = d.cluster.ContainerConfigRemove(id, "volatile.apply_template")
		if err != nil {
			return errors.Wrap(err, "Failed to remove volatile.apply_template config key")
		}

		if origVolatileApplyTemplate != "" {
			config := map[string]string{
				"volatile.apply_template": origVolatileApplyTemplate,
			}
			err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
				return tx.ContainerConfigInsert(id, config)
			})
			if err != nil {
				return errors.Wrap(err, "Failed to set volatile.apply_template config key")
			}
		}

		return nil
	}

	resources := map[string][]string{}
	resources["containers"] = []string{oldName}
	op, err := operationCreate(d.cluster, c.Project(), operationClassTask, db.OperationContainerMigrate, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

// Special case migrating a container backed by ceph across two cluster nodes.
func containerPostClusteringMigrateWithCeph(d *Daemon, c container, project, oldName, newName, newNode string) Response {
	run := func(*operation) error {
		// If source node is online (i.e. we're serving the request on
		// it, and c != nil), let's unmap the RBD volume locally
		if c != nil {
			logger.Debugf(`Renaming RBD storage volume for source container "%s" from "%s" to "%s"`, c.Name(), c.Name(), newName)
			poolName, err := c.StoragePool()
			if err != nil {
				return errors.Wrap(err, "Failed to get source container's storage pool name")
			}
			_, pool, err := d.cluster.StoragePoolGet(poolName)
			if err != nil {
				return errors.Wrap(err, "Failed to get source container's storage pool")
			}
			if pool.Driver != "ceph" {
				return fmt.Errorf("Source container's storage pool is not of type ceph")
			}
			si, err := storagePoolVolumeContainerLoadInit(d.State(), c.Project(), c.Name())
			if err != nil {
				return errors.Wrap(err, "Failed to initialize source container's storage pool")
			}
			s, ok := si.(*storageCeph)
			if !ok {
				return fmt.Errorf("Unexpected source container storage backend")
			}
			err = cephRBDVolumeUnmap(s.ClusterName, s.OSDPoolName, c.Name(),
				storagePoolVolumeTypeNameContainer, s.UserName, true)
			if err != nil {
				return errors.Wrap(err, "Failed to unmap source container's RBD volume")
			}

		}

		// Re-link the database entries against the new node name.
		var poolName string
		err := d.cluster.Transaction(func(tx *db.ClusterTx) error {
			err := tx.ContainerNodeMove(oldName, newName, newNode)
			if err != nil {
				return err
			}
			poolName, err = tx.ContainerPool(project, newName)
			if err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return errors.Wrap(err, "Failed to relink container database data")
		}

		// Rename the RBD volume if necessary.
		if newName != oldName {
			s := storageCeph{}
			_, s.pool, err = d.cluster.StoragePoolGet(poolName)
			if err != nil {
				return errors.Wrap(err, "Failed to get storage pool")
			}
			if err != nil {
				return errors.Wrap(err, "Failed to get storage pool")
			}
			err = s.StoragePoolInit()
			if err != nil {
				return errors.Wrap(err, "Failed to initialize ceph storage pool")
			}
			err = cephRBDVolumeRename(s.ClusterName, s.OSDPoolName,
				storagePoolVolumeTypeNameContainer, oldName, newName, s.UserName)
			if err != nil {
				return errors.Wrap(err, "Failed to rename ceph RBD volume")
			}
		}

		// Create the container mount point on the target node
		cert := d.endpoints.NetworkCert()
		client, err := cluster.ConnectIfContainerIsRemote(d.cluster, project, newName, cert)
		if err != nil {
			return errors.Wrap(err, "Failed to connect to target node")
		}
		if client == nil {
			err := containerPostCreateContainerMountPoint(d, project, newName)
			if err != nil {
				return errors.Wrap(err, "Failed to create mount point on target node")
			}
		} else {
			path := fmt.Sprintf("/internal/cluster/container-moved/%s", newName)
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
	op, err := operationCreate(d.cluster, project, operationClassTask, db.OperationContainerMigrate, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

// Notification that a container was moved.
//
// At the moment it's used for ceph-based containers, where the target node needs
// to create the appropriate mount points.
func internalClusterContainerMovedPost(d *Daemon, r *http.Request) Response {
	project := projectParam(r)
	containerName := mux.Vars(r)["name"]
	err := containerPostCreateContainerMountPoint(d, project, containerName)
	if err != nil {
		return SmartError(err)
	}
	return EmptySyncResponse
}

// Used after to create the appropriate mounts point after a container has been
// moved.
func containerPostCreateContainerMountPoint(d *Daemon, project, containerName string) error {
	c, err := containerLoadByProjectAndName(d.State(), project, containerName)
	if err != nil {
		return errors.Wrap(err, "Failed to load moved container on target node")
	}
	poolName, err := c.StoragePool()
	if err != nil {
		return errors.Wrap(err, "Failed get pool name of moved container on target node")
	}
	snapshotNames, err := d.cluster.ContainerGetSnapshots(project, containerName)
	if err != nil {
		return errors.Wrap(err, "Failed to create container snapshot names")
	}

	containerMntPoint := getContainerMountPoint(c.Project(), poolName, containerName)
	err = createContainerMountpoint(containerMntPoint, c.Path(), c.IsPrivileged())
	if err != nil {
		return errors.Wrap(err, "Failed to create container mount point on target node")
	}

	for _, snapshotName := range snapshotNames {
		mntPoint := getSnapshotMountPoint(project, poolName, snapshotName)
		snapshotsSymlinkTarget := shared.VarPath("storage-pools",
			poolName, "containers-snapshots", containerName)
		snapshotMntPointSymlink := shared.VarPath("snapshots", containerName)
		err := createSnapshotMountpoint(mntPoint, snapshotsSymlinkTarget, snapshotMntPointSymlink)
		if err != nil {
			return errors.Wrap(err, "Failed to create snapshot mount point on target node")
		}
	}

	return nil
}
