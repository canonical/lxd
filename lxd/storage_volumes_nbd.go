package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	lxdCluster "github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/operations"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	storagePools "github.com/canonical/lxd/lxd/storage"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/revert"
)

var storagePoolVolumeTypeNBDCmd = APIEndpoint{
	Path:            "storage-pools/{poolName}/volumes/{type}/{volumeName}/nbd",
	MetricsType:     entity.TypeStoragePool,
	ProjectSpecific: true,

	Post: APIEndpointAction{Handler: storagePoolVolumeTypeNBDPost, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanConnectNBD)},
}

// storagePoolVolumeTypeNBDForward connects to the member of the virtual machine that uses the
// volume when it is not the local one, as qemu-nbd opens the volume there.
// An upgraded connection cannot be proxied with the usual forwarded response.
// The caller therefore opens the NBD connection through the returned client instead.
// A nil client means the export is served locally.
// When the returned response is non-nil, the caller should return it immediately.
func storagePoolVolumeTypeNBDForward(s *state.State, r *http.Request, details storageVolumeDetails, effectiveProjectName string) (lxd.InstanceServer, response.Response) {
	var address string
	var err error
	target := request.QueryParam(r, "target")
	if target != "" {
		address, err = lxdCluster.ResolveTarget(r.Context(), s, target)
		if err != nil {
			return nil, response.SmartError(err)
		}
	} else if details.forwardingNodeInfo != nil {
		address = details.forwardingNodeInfo.Address
	}

	if address != "" {
		client, err := lxdCluster.Connect(r.Context(), address, s.Endpoints.NetworkCert(), s.ServerCert(), false)
		if err != nil {
			return nil, response.SmartError(err)
		}

		return client.UseProject(request.ProjectParam(r)), nil
	}

	// A remote pool records no location for its volumes.
	// The access handler therefore cannot forward them through forwardingNodeInfo the way it does
	// for volumes on local pools.
	// The root volume of a virtual machine is served by the member of the instance.
	if details.volumeType == cluster.StoragePoolVolumeTypeVM {
		client, err := lxdCluster.ConnectIfInstanceIsRemote(r.Context(), s, effectiveProjectName, details.volumeName, instancetype.VM)
		if err != nil {
			return nil, response.SmartError(err)
		}

		return client, nil
	}

	// A custom volume attached to a virtual machine is served by the member of that instance,
	// whose config volume stores the bitmaps of the volume.
	// A targeted or forwarded request is not forwarded again.
	// This keeps two members from forwarding it back and forth.
	inst, _, err := storagePools.InstanceByVolumeName(s, details.pool.Name(), effectiveProjectName, details.volumeName, details.volumeType)
	if err != nil {
		if errors.Is(err, storagePools.ErrVolumeNotAttached) {
			return nil, nil
		}

		return nil, response.SmartError(err)
	}

	if inst.Location() == s.ServerName {
		return nil, nil
	}

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return nil, response.SmartError(err)
	}

	if requestor.IsForwarded() {
		return nil, response.BadRequest(fmt.Errorf("Instance %q is located on cluster member %q", inst.Name(), inst.Location()))
	}

	address, err = lxdCluster.ResolveTarget(r.Context(), s, inst.Location())
	if err != nil {
		return nil, response.SmartError(err)
	}

	client, err := lxdCluster.Connect(r.Context(), address, s.Endpoints.NetworkCert(), s.ServerCert(), false)
	if err != nil {
		return nil, response.SmartError(err)
	}

	return client.UseProject(request.ProjectParam(r)), nil
}

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/nbd storage storage_pool_volumes_type_nbd_post
//
//	Write the storage volume over NBD
//
//	Upgrades the request to a read-write NBD connection of the storage volume's block device, for writing a backup
//	back. The volume must be of type virtual-machine or custom, and the virtual machine that uses the volume must be
//	stopped. The bitmaps of the volume are deleted before the export starts, as every write invalidates them.
//	The export runs as an operation on the cluster member serving it, which is listed and cancelled through
//	the operations API. Cancelling the operation closes the connection.
//
//	---
//	produces:
//	  - application/json
//	  - application/octet-stream
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: lxd01
//	responses:
//	  "101":
//	    description: Switching protocols to NBD
//	    headers:
//	      Location:
//	        description: URL of the operation representing the export
//	        type: string
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeNBDPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	if r.Header.Get("Upgrade") != "nbd" {
		return response.SmartError(api.StatusErrorf(http.StatusBadRequest, "Missing or invalid upgrade header"))
	}

	details, err := request.GetContextValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if !slices.Contains([]cluster.StoragePoolVolumeType{cluster.StoragePoolVolumeTypeVM, cluster.StoragePoolVolumeTypeCustom}, details.volumeType) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	effectiveProjectName, err := request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return response.SmartError(err)
	}

	client, resp := storagePoolVolumeTypeNBDForward(s, r, details, effectiveProjectName)
	if resp != nil {
		return resp
	}

	if client != nil {
		conn, operationURL, err := client.GetStoragePoolVolumeNBDConn(details.pool.Name(), details.volumeTypeName, details.volumeName)
		if err != nil {
			return response.SmartError(err)
		}

		// The serving member runs the operation representing the export.
		return response.UpgradeResponse(conn, "nbd", operationURL, nil)
	}

	conn, cleanup, conflictReference, err := details.pool.GetVolumeNBD(effectiveProjectName, storagePools.VolumeDBTypeToType(details.volumeType), details.volumeName)
	if err != nil {
		return response.SmartError(err)
	}

	// The relay closes the connection and runs cleanup once the operation runs it.
	revert := revert.New()
	defer revert.Fail()
	revert.Add(func() {
		_ = conn.Close()
		cleanup()
	})

	relay := response.NewUpgradeRelay(conn, "nbd", cleanup)

	run := func(ctx context.Context, _ *operations.Operation) error {
		return relay.Run(ctx)
	}

	// The operation takes the conflict reference of the session.
	// This keeps other cluster members from exporting the same volume at the same time.
	opArgs := operations.OperationArgs{
		ProjectName:       request.ProjectParam(r),
		EntityURL:         entity.StorageVolumeURL(effectiveProjectName, details.location, details.pool.Name(), details.volumeTypeName, details.volumeName),
		Type:              operationtype.VolumeNBDImport,
		Class:             operationtype.OperationClassTask,
		RunHook:           run,
		ConflictReference: conflictReference,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, opArgs)
	if err != nil {
		return response.SmartError(err)
	}

	revert.Success()
	return relay.Response(op)
}
