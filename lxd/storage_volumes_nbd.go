package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd/auth"
	lxdCluster "github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/db/operationtype"
	"github.com/canonical/lxd/lxd/instance"
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

	Get:  APIEndpointAction{Handler: storagePoolVolumeTypeNBDGet, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanConnectNBD)},
	Post: APIEndpointAction{Handler: storagePoolVolumeTypeNBDPost, AccessHandler: storagePoolVolumeTypeAccessHandler(auth.EntitlementCanConnectNBD)},
}

// storagePoolVolumeTypeNBDForward applies the checks shared by both NBD endpoints, decodes the request body into
// args and connects to the cluster member that must serve the export when it is not the local one. An upgraded
// connection cannot be proxied with the usual forwarded response, so the caller opens the NBD connection through
// the returned client instead. A nil client means the export is served locally. When the returned response is
// non-nil, the caller should return it immediately. writable selects the import path, which the pool serves
// where the request lands.
func storagePoolVolumeTypeNBDForward(s *state.State, r *http.Request, writable bool, args any) (client lxd.InstanceServer, details storageVolumeDetails, effectiveProjectName string, resp response.Response) {
	if r.Header.Get("Upgrade") != "nbd" {
		return nil, details, "", response.SmartError(api.StatusErrorf(http.StatusBadRequest, "Missing or invalid upgrade header"))
	}

	details, err := request.GetContextValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return nil, details, "", response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if !slices.Contains([]cluster.StoragePoolVolumeType{cluster.StoragePoolVolumeTypeVM, cluster.StoragePoolVolumeTypeCustom}, details.volumeType) {
		return nil, details, "", response.BadRequest(fmt.Errorf("Invalid storage volume type %q", details.volumeTypeName))
	}

	// An empty body selects the defaults.
	err = json.NewDecoder(r.Body).Decode(args)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, details, "", response.BadRequest(err)
	}

	effectiveProjectName, err = request.GetContextValue[string](r.Context(), request.CtxEffectiveProjectName)
	if err != nil {
		return nil, details, "", response.SmartError(err)
	}

	// Forward if needed.
	var address string
	target := request.QueryParam(r, "target")
	if target != "" {
		address, err = lxdCluster.ResolveTarget(r.Context(), s, target)
		if err != nil {
			return nil, details, "", response.SmartError(err)
		}
	} else if details.forwardingNodeInfo != nil {
		address = details.forwardingNodeInfo.Address
	}

	if address != "" {
		client, err = lxdCluster.Connect(r.Context(), address, s.Endpoints.NetworkCert(), s.ServerCert(), false)
		if err != nil {
			return nil, details, "", response.SmartError(err)
		}

		return client.UseProject(request.ProjectParam(r)), details, effectiveProjectName, nil
	}

	// A remote pool records no location for its volumes, so the access handler cannot forward them through
	// forwardingNodeInfo the way it does for volumes on local pools. A virtual machine volume on a remote pool
	// is served by the member running the instance.
	if details.volumeType == cluster.StoragePoolVolumeTypeVM {
		client, err = lxdCluster.ConnectIfInstanceIsRemote(r.Context(), s, effectiveProjectName, details.volumeName, instancetype.VM)
		if err != nil {
			return nil, details, "", response.SmartError(err)
		}

		return client, details, effectiveProjectName, nil
	}

	// A writable export requires every instance using the volume to be stopped and on the serving member, which
	// the pool checks, as a shared volume may be attached to several of them.
	if writable {
		return nil, details, effectiveProjectName, nil
	}

	// A custom volume on a remote pool is read through the QEMU process of the instance it is attached to, so
	// the member running that instance serves it. A targeted or forwarded request is not forwarded again, so
	// that two members never bounce it between them.
	inst, _, err := storagePools.InstanceByVolumeName(s, details.pool.Name(), effectiveProjectName, details.volumeName, details.volumeType)
	if err != nil {
		if errors.Is(err, storagePools.ErrVolumeNotAttached) {
			return nil, details, effectiveProjectName, nil
		}

		return nil, details, "", response.SmartError(err)
	}

	if inst.Location() == s.ServerName {
		return nil, details, effectiveProjectName, nil
	}

	requestor, err := request.GetRequestor(r.Context())
	if err != nil {
		return nil, details, "", response.SmartError(err)
	}

	if target != "" || requestor.IsForwarded() {
		return nil, details, "", response.BadRequest(fmt.Errorf("Instance %q is located on cluster member %q", inst.Name(), inst.Location()))
	}

	address, err = lxdCluster.ResolveTarget(r.Context(), s, inst.Location())
	if err != nil {
		return nil, details, "", response.SmartError(err)
	}

	client, err = lxdCluster.Connect(r.Context(), address, s.Endpoints.NetworkCert(), s.ServerCert(), false)
	if err != nil {
		return nil, details, "", response.SmartError(err)
	}

	return client.UseProject(request.ProjectParam(r)), details, effectiveProjectName, nil
}

// storagePoolVolumeTypeNBDConnect opens the NBD connection to the volume on the local member.
func storagePoolVolumeTypeNBDConnect(s *state.State, details storageVolumeDetails, effectiveProjectName string, writable bool, reuse bool) (net.Conn, func(), error) {
	if details.volumeType == cluster.StoragePoolVolumeTypeVM {
		inst, err := instance.LoadByProjectAndName(s, effectiveProjectName, details.volumeName)
		if err != nil {
			return nil, nil, err
		}

		return details.pool.GetInstanceNBD(inst, writable, reuse)
	}

	return details.pool.GetCustomVolumeNBD(effectiveProjectName, details.volumeName, writable, reuse)
}

// storagePoolVolumeTypeNBDServe opens the NBD connection to the volume on the local member and returns the
// response relaying it. The relay runs inside an operation of type opType, which is listed and cancelled through
// the operations API, and the 101 response names the operation in its Location header.
func storagePoolVolumeTypeNBDServe(s *state.State, r *http.Request, details storageVolumeDetails, effectiveProjectName string, opType operationtype.Type, writable bool, reuse bool) response.Response {
	conn, cleanup, err := storagePoolVolumeTypeNBDConnect(s, details, effectiveProjectName, writable, reuse)
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

	args := operations.OperationArgs{
		ProjectName: request.ProjectParam(r),
		EntityURL:   entity.StorageVolumeURL(effectiveProjectName, details.location, details.pool.Name(), details.volumeTypeName, details.volumeName),
		Type:        opType,
		Class:       operationtype.OperationClassTask,
		RunHook:     run,
	}

	op, err := operations.ScheduleUserOperationFromRequest(s, r, args)
	if err != nil {
		return response.InternalError(err)
	}

	revert.Success()
	return relay.Response(op)
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/nbd storage storage_pool_volumes_type_nbd_get
//
//	Get the storage volume NBD connection
//
//	Upgrades the request to a read-only NBD connection of the storage volume's block device.
//	The volume must be of type virtual-machine or custom and attached to a running virtual machine.
//	The export runs as an operation on the cluster member serving it, which is listed and cancelled through
//	the operations API. Cancelling the operation closes the connection.
//
//	---
//	consumes:
//	  - application/json
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
//	  - in: body
//	    name: nbd
//	    description: NBD export
//	    required: false
//	    schema:
//	      $ref: "#/definitions/StorageVolumeNBDGet"
//	responses:
//	  "101":
//	    description: Switching protocols to NBD
//	    headers:
//	      Location:
//	        description: URL of the operation representing the export, set when the responding cluster member serves it
//	        type: string
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeNBDGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	args := api.StorageVolumeNBDGet{}
	client, details, effectiveProjectName, resp := storagePoolVolumeTypeNBDForward(s, r, false, &args)
	if resp != nil {
		return resp
	}

	if client != nil {
		conn, err := client.GetStoragePoolVolumeNBDConn(details.pool.Name(), details.volumeTypeName, details.volumeName, args)
		if err != nil {
			return response.SmartError(err)
		}

		// The serving member runs the operation representing the export.
		return response.UpgradeResponse(conn, "nbd", nil)
	}

	return storagePoolVolumeTypeNBDServe(s, r, details, effectiveProjectName, operationtype.VolumeNBDExport, false, args.Reuse)
}

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/nbd storage storage_pool_volumes_type_nbd_post
//
//	Import into the storage volume over NBD
//
//	Upgrades the request to a writable NBD connection of the storage volume's block device.
//	The volume must be of type virtual-machine or custom and every instance using it must be stopped.
//	The import runs as an operation on the cluster member serving it, which is listed and cancelled through
//	the operations API. Cancelling the operation closes the connection.
//
//	---
//	consumes:
//	  - application/json
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
//	  - in: body
//	    name: nbd
//	    description: NBD import
//	    required: false
//	    schema:
//	      $ref: "#/definitions/StorageVolumeNBDPost"
//	responses:
//	  "101":
//	    description: Switching protocols to NBD
//	    headers:
//	      Location:
//	        description: URL of the operation representing the import, set when the responding cluster member serves it
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

	args := api.StorageVolumeNBDPost{}
	client, details, effectiveProjectName, resp := storagePoolVolumeTypeNBDForward(s, r, true, &args)
	if resp != nil {
		return resp
	}

	if client != nil {
		conn, err := client.GetStoragePoolVolumeNBDWriteConn(details.pool.Name(), details.volumeTypeName, details.volumeName, args)
		if err != nil {
			return response.SmartError(err)
		}

		// The serving member runs the operation representing the import.
		return response.UpgradeResponse(conn, "nbd", nil)
	}

	return storagePoolVolumeTypeNBDServe(s, r, details, effectiveProjectName, operationtype.VolumeNBDImport, true, false)
}
