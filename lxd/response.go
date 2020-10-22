package main

import (
	"net/http"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/response"
)

// forwardedResponseIfTargetIsRemote redirects a request to the request has a
// targetNode parameter pointing to a node which is not the local one.
func forwardedResponseIfTargetIsRemote(d *Daemon, request *http.Request) response.Response {
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

// forwardedResponseIfInstanceIsRemote redirects a request to the node running
// the container with the given name. If the container is local, nothing gets
// done and nil is returned.
func forwardedResponseIfInstanceIsRemote(d *Daemon, r *http.Request, project, name string, instanceType instancetype.Type) (response.Response, error) {
	cert := d.endpoints.NetworkCert()
	client, err := cluster.ConnectIfInstanceIsRemote(d.cluster, project, name, cert, instanceType)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, nil
	}
	return response.ForwardedResponse(client, r), nil
}

// forwardedResponseIfVolumeIsRemote redirects a request to the node hosting
// the volume with the given pool ID, name and type. If the container is local,
// nothing gets done and nil is returned. If more than one node has a matching
// volume, an error is returned.
//
// This is used when no targetNode is specified, and saves users some typing
// when the volume name/type is unique to a node.
func forwardedResponseIfVolumeIsRemote(d *Daemon, r *http.Request, poolID int64, projectName string, volumeName string, volumeType int) response.Response {
	if queryParam(r, "target") != "" {
		return nil
	}

	cert := d.endpoints.NetworkCert()
	client, err := cluster.ConnectIfVolumeIsRemote(d.cluster, poolID, projectName, volumeName, volumeType, cert)
	if err != nil && err != db.ErrNoSuchObject {
		return response.SmartError(err)
	}
	if client == nil {
		return nil
	}
	return response.ForwardedResponse(client, r)
}
