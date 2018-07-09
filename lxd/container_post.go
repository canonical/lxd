package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"

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
	name := mux.Vars(r)["name"]
	targetNode := r.FormValue("target")

	sourceNodeOffline := false
	targetNodeOffline := false

	// A POST to /containers/<name>?target=<node> is meant to be
	// used to move a container backed by a ceph storage pool.
	if targetNode != "" {
		// Determine if either the source node (the one currently
		// running the container) or the target node are offline.
		//
		// If the target node is offline, we return an error.
		//
		// If the source node is offline, we'll assume that the
		// container is not running and it's safe to move it.
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
			address, err := tx.ContainerNodeAddress(name)
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

	// For in-cluster migrations, only forward the request to the source
	// node and load the container if the source node is online. We'll not
	// check whether the container is running or try to unmap the RBD
	// volume on it if the source node is offline.
	if targetNode == "" || !sourceNodeOffline {
		// Handle requests targeted to a container on a different node
		response, err := ForwardedResponseIfContainerIsRemote(d, r, name)
		if err != nil {
			return SmartError(err)
		}
		if response != nil {
			return response
		}

		c, err = containerLoadByName(d.State(), name)
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
			// Check if we are migrating a ceph-based container.
			poolName, err := d.cluster.ContainerPool(name)
			if err != nil {
				return SmartError(err)
			}
			_, pool, err := d.cluster.StoragePoolGet(poolName)
			if err != nil {
				return SmartError(err)
			}
			if pool.Driver == "ceph" {
				return containerPostClusteringMigrateWithCeph(d, c, name, req.Name, targetNode)
			}
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

			op, err := operationCreate(d.cluster, operationClassTask, "Migrating container", resources, nil, ws.Do, nil, nil)
			if err != nil {
				return InternalError(err)
			}

			return OperationResponse(op)
		}

		// Pull mode
		op, err := operationCreate(d.cluster, operationClassWebsocket, "Migrating container", resources, ws.Metadata(), ws.Do, nil, ws.Connect)
		if err != nil {
			return InternalError(err)
		}

		return OperationResponse(op)
	}

	// Check that the name isn't already in use
	id, _ := d.cluster.ContainerID(req.Name)
	if id > 0 {
		return Conflict
	}

	run := func(*operation) error {
		return c.Rename(req.Name)
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operationCreate(d.cluster, operationClassTask, "Renaming container", resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}

// Special case migrating a container backed by ceph across two cluster nodes.
func containerPostClusteringMigrateWithCeph(d *Daemon, c container, oldName, newName, newNode string) Response {
	if c != nil && c.IsRunning() {
		return BadRequest(fmt.Errorf("Container is running"))
	}

	run := func(*operation) error {
		// If source node is online (i.e. we're serving the request on
		// it, and c != nil), let's unmap the RBD volume locally
		if c != nil {
			logger.Debugf(`Renaming RBD storage volume for source container "%s" from `+
				`"%s" to "%s"`, c.Name(), c.Name(), newName)
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
			si, err := storagePoolVolumeContainerLoadInit(d.State(), c.Name())
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
			poolName, err = tx.ContainerPool(newName)
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
		client, err := cluster.ConnectIfContainerIsRemote(d.cluster, newName, cert)
		if err != nil {
			return errors.Wrap(err, "Failed to connect to target node")
		}
		if client == nil {
			err := containerPostCreateContainerMountPoint(d, newName)
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
	op, err := operationCreate(d.cluster, operationClassTask, "Moving container", resources, nil, run, nil, nil)
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
	containerName := mux.Vars(r)["name"]
	err := containerPostCreateContainerMountPoint(d, containerName)
	if err != nil {
		return SmartError(err)
	}
	return EmptySyncResponse
}

// Used after to create the appropriate mounts point after a container has been
// moved.
func containerPostCreateContainerMountPoint(d *Daemon, containerName string) error {
	c, err := containerLoadByName(d.State(), containerName)
	if err != nil {
		return errors.Wrap(err, "Failed to load moved container on target node")
	}
	poolName, err := c.StoragePool()
	if err != nil {
		return errors.Wrap(err, "Failed get pool name of moved container on target node")
	}
	snapshotNames, err := d.cluster.ContainerGetSnapshots(containerName)
	if err != nil {
		return errors.Wrap(err, "Failed to create container snapshot names")
	}

	containerMntPoint := getContainerMountPoint(poolName, containerName)
	err = createContainerMountpoint(containerMntPoint, c.Path(), c.IsPrivileged())
	if err != nil {
		return errors.Wrap(err, "Failed to create container mount point on target node")
	}

	for _, snapshotName := range snapshotNames {
		mntPoint := getSnapshotMountPoint(poolName, snapshotName)
		snapshotsSymlinkTarget := shared.VarPath("storage-pools",
			poolName, "snapshots", containerName)
		snapshotMntPointSymlink := shared.VarPath("snapshots", containerName)
		err := createSnapshotMountpoint(mntPoint, snapshotsSymlinkTarget, snapshotMntPointSymlink)
		if err != nil {
			return errors.Wrap(err, "Failed to create snapshot mount point on target node")
		}
	}

	return nil
}
