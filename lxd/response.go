package main

import (
	"fmt"
	"net/http"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
)

// ForwardedResponseIfTargetIsRemote redirects a request to the request has a
// targetNode parameter pointing to a node which is not the local one.
func ForwardedResponseIfTargetIsRemote(d *Daemon, request *http.Request) response.Response {
	targetNode := queryParam(request, "target")
	if targetNode == "" {
		return nil
	}

	// Figure out the address of the target node (which is possibly
	// this very same node).
	address, err := cluster.ResolveTarget(d.cluster, targetNode)
	if err != nil {
		return response.SmartError(err)
	}

	if address != "" {
		// Forward the response.
		cert := d.endpoints.NetworkCert()
		client, err := cluster.Connect(address, cert, false)
		if err != nil {
			return response.SmartError(err)
		}
		return response.ForwardedResponse(client, request)
	}

	return nil
}

// ForwardedResponseIfContainerIsRemote redirects a request to the node running
// the container with the given name. If the container is local, nothing gets
// done and nil is returned.
func ForwardedResponseIfContainerIsRemote(d *Daemon, r *http.Request, project, name string, instanceType instancetype.Type) (response.Response, error) {
	cert := d.endpoints.NetworkCert()
	client, err := cluster.ConnectIfContainerIsRemote(d.cluster, project, name, cert, instanceType)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, nil
	}
	return response.ForwardedResponse(client, r), nil
}

// ForwardedResponseIfVolumeIsRemote redirects a request to the node hosting
// the volume with the given pool ID, name and type. If the container is local,
// nothing gets done and nil is returned. If more than one node has a matching
// volume, an error is returned.
//
// This is used when no targetNode is specified, and saves users some typing
// when the volume name/type is unique to a node.
func ForwardedResponseIfVolumeIsRemote(d *Daemon, r *http.Request, poolID int64, volumeName string, volumeType int) response.Response {
	if queryParam(r, "target") != "" {
		return nil
	}

	cert := d.endpoints.NetworkCert()
	client, err := cluster.ConnectIfVolumeIsRemote(d.cluster, poolID, volumeName, volumeType, cert)
	if err != nil && err != db.ErrNoSuchObject {
		return response.SmartError(err)
	}
	if client == nil {
		return nil
	}
	return response.ForwardedResponse(client, r)
}

// Operation response
type operationResponse struct {
	op *operation
}

// OperationResponse returns an operation response.
func OperationResponse(op *operation) response.Response {
	return &operationResponse{op}
}

func (r *operationResponse) Render(w http.ResponseWriter) error {
	_, err := r.op.Run()
	if err != nil {
		return err
	}

	url, md, err := r.op.Render()
	if err != nil {
		return err
	}

	body := api.ResponseRaw{
		Type:       api.AsyncResponse,
		Status:     api.OperationCreated.String(),
		StatusCode: int(api.OperationCreated),
		Operation:  url,
		Metadata:   md,
	}

	w.Header().Set("Location", url)
	w.WriteHeader(202)

	return util.WriteJSON(w, body, debug)
}

func (r *operationResponse) String() string {
	_, md, err := r.op.Render()
	if err != nil {
		return fmt.Sprintf("error: %s", err)
	}

	return md.ID
}

// Forwarded operation response.
//
// Returned when the operation has been created on another node
type forwardedOperationResponse struct {
	op      *api.Operation
	project string
}

// ForwardedOperationResponse creates a response that forwards the metadata of
// an operation created on another node.
func ForwardedOperationResponse(project string, op *api.Operation) response.Response {
	return &forwardedOperationResponse{
		op:      op,
		project: project,
	}
}

func (r *forwardedOperationResponse) Render(w http.ResponseWriter) error {
	url := fmt.Sprintf("/%s/operations/%s", version.APIVersion, r.op.ID)
	if r.project != "" {
		url += fmt.Sprintf("?project=%s", r.project)
	}

	body := api.ResponseRaw{
		Type:       api.AsyncResponse,
		Status:     api.OperationCreated.String(),
		StatusCode: int(api.OperationCreated),
		Operation:  url,
		Metadata:   r.op,
	}

	w.Header().Set("Location", url)
	w.WriteHeader(202)

	return util.WriteJSON(w, body, debug)
}

func (r *forwardedOperationResponse) String() string {
	return r.op.ID
}
