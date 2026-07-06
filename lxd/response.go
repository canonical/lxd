package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
)

// forwardedResponseToNode forwards the request to the specified cluster member.
// If the member name is empty or matches the current cluster member, nil is returned.
func forwardedResponseToNode(ctx context.Context, s *state.State, memberName string) response.Response {
	// Do nothing if cluster member name is empty.
	if memberName == "" {
		return nil
	}

	// Figure out the address of the target member (which is possibly this very same member).
	address, err := cluster.ResolveTarget(ctx, s, memberName)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward the response if not local.
	if address != "" {
		client, err := cluster.Connect(ctx, address, s.Endpoints.NetworkCert(), s.ServerCert(), false)
		if err != nil {
			return response.SmartError(err)
		}

		return response.ForwardedResponse(client)
	}

	return nil
}

// forwardedResponseIfInstanceIsRemote redirects a request to the node running
// the container with the given name. If the container is local, nothing gets
// done and nil is returned.
func forwardedResponseIfInstanceIsRemote(ctx context.Context, s *state.State, project, name string, instanceType instancetype.Type) (response.Response, error) {
	client, err := cluster.ConnectIfInstanceIsRemote(ctx, s, project, name, instanceType)
	if err != nil {
		return nil, err
	}

	if client == nil {
		return nil, nil
	}

	return response.ForwardedResponse(client), nil
}

// forwardedInstanceResponse detects the instance type from the request, extracts the project and
// instance name, rejects snapshot names, and forwards the request to the cluster member running the
// instance if it is remote. When the returned response is non-nil, the caller should return it
// immediately: it is either an error response, a bad request for a snapshot name, or the forwarded
// response from the remote member. When it is nil, the instance is local and the caller should
// continue using the returned project and instance name.
func forwardedInstanceResponse(s *state.State, r *http.Request) (projectName string, name string, resp response.Response) {
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return "", "", response.SmartError(err)
	}

	projectName = request.ProjectParam(r)
	name = r.PathValue("name")
	if shared.IsSnapshot(name) {
		return projectName, name, response.BadRequest(errors.New("Invalid instance name"))
	}

	forwarded, err := forwardedResponseIfInstanceIsRemote(r.Context(), s, projectName, name, instanceType)
	if err != nil {
		return projectName, name, response.SmartError(err)
	}

	return projectName, name, forwarded
}

// forwardedResponseIfVolumeIsRemote checks for the presence of the ctxStorageVolumeRemoteNodeInfo key in the context.
// If it is present, the db.NodeInfo value for this key is used to set up a client for the indicated member and forward the request.
// Otherwise, a nil response is returned to indicate that the request was not forwarded, and should continue within this member.
func forwardedResponseIfVolumeIsRemote(ctx context.Context, s *state.State) response.Response {
	storageVolumeDetails, err := request.GetContextValue[storageVolumeDetails](ctx, ctxStorageVolumeDetails)
	if err != nil {
		return nil
	} else if storageVolumeDetails.forwardingNodeInfo == nil {
		return nil
	}

	client, err := cluster.Connect(ctx, storageVolumeDetails.forwardingNodeInfo.Address, s.Endpoints.NetworkCert(), s.ServerCert(), false)
	if err != nil {
		return response.SmartError(err)
	}

	return response.ForwardedResponse(client)
}
