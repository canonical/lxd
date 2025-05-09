package main

import (
	"context"
	"net/http"

	"github.com/canonical/lxd/lxd/cluster"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/lxd/state"
)

func forwardedResponseToNode(s *state.State, r *http.Request, memberName string) response.Response {
	// Figure out the address of the target member (which is possibly this very same member).
	address, err := cluster.ResolveTarget(r.Context(), s, memberName)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward the response if not local.
	if address != "" {
		client, err := cluster.Connect(r.Context(), address, s.Endpoints.NetworkCert(), s.ServerCert(), false)
		if err != nil {
			return response.SmartError(err)
		}

		return response.ForwardedResponse(client)
	}

	return nil
}

// forwardedResponseIfTargetIsRemote forwards a request to the request has a target parameter pointing to a member
// which is not the local one.
func forwardedResponseIfTargetIsRemote(s *state.State, r *http.Request) response.Response {
	targetNode := request.QueryParam(r, "target")
	if targetNode == "" {
		return nil
	}

	return forwardedResponseToNode(s, r, targetNode)
}

// forwardedResponseIfInstanceIsRemote redirects a request to the node running
// the container with the given name. If the container is local, nothing gets
// done and nil is returned.
func forwardedResponseIfInstanceIsRemote(s *state.State, r *http.Request, project, name string, instanceType instancetype.Type) (response.Response, error) {
	client, err := cluster.ConnectIfInstanceIsRemote(r.Context(), s, project, name, instanceType)
	if err != nil {
		return nil, err
	}

	if client == nil {
		return nil, nil
	}

	return response.ForwardedResponse(client), nil
}

// forwardedResponseIfVolumeIsRemote checks for the presence of the ctxStorageVolumeRemoteNodeInfo key in the request context.
// If it is present, the db.NodeInfo value for this key is used to set up a client for the indicated member and forward the request.
// Otherwise, a nil response is returned to indicate that the request was not forwarded, and should continue within this member.
func forwardedResponseIfVolumeIsRemote(s *state.State, r *http.Request) response.Response {
	storageVolumeDetails, err := request.GetCtxValue[storageVolumeDetails](r.Context(), ctxStorageVolumeDetails)
	if err != nil {
		return nil
	} else if storageVolumeDetails.forwardingNodeInfo == nil {
		return nil
	}

	client, err := cluster.Connect(r.Context(), storageVolumeDetails.forwardingNodeInfo.Address, s.Endpoints.NetworkCert(), s.ServerCert(), false)
	if err != nil {
		return response.SmartError(err)
	}

	return response.ForwardedResponse(client)
}

func forwardToAddress(reqContext context.Context, s *state.State, address string) error {
	// Empty address indicates there is no need to forward the request.
	if address == "" {
		return nil
	}

	forwarder := func() response.Response {
		client, err := cluster.Connect(reqContext, address, s.Endpoints.NetworkCert(), s.ServerCert(), false)
		if err != nil {
			return response.SmartError(err)
		}

		return response.ForwardedResponse(client)
	}

	return response.NewRequestForwardRequiredError(address, forwarder)
}

// forwardToNode returns a forward request error if the request needs to be forwarded to another cluster member.
func forwardToNode(reqContext context.Context, s *state.State, memberName string) error {
	// Figure out the address of the target member (which is possibly this very same member).
	address, err := cluster.ResolveTarget(reqContext, s, memberName)
	if err != nil {
		return err
	}

	return forwardToAddress(reqContext, s, address)
}

// forwardIfVolumeIsRemote returns a forward request error if the volume is not available on the local member.
func forwardIfTargetIsRemote(reqContext context.Context, s *state.State, target string) error {
	if target == "" {
		return nil
	}

	return forwardToNode(reqContext, s, target)
}

// forwardIfVolumeIsRemote returns a forward request error if the volume is not available on the local member.
func forwardIfVolumeIsRemote(reqContext context.Context, s *state.State) error {
	storageVolumeDetails, err := request.GetCtxValue[storageVolumeDetails](reqContext, ctxStorageVolumeDetails)
	if err != nil {
		return nil
	}

	if storageVolumeDetails.forwardingNodeInfo == nil {
		return nil
	}

	return forwardToAddress(reqContext, s, storageVolumeDetails.forwardingNodeInfo.Address)
}

// forwardIfInstanceIsRemote returns a forward request error if the instance is not available on the local member.
func forwardIfInstanceIsRemote(reqContext context.Context, s *state.State, project string, name string, instanceType instancetype.Type) error {
	client, err := cluster.ConnectIfInstanceIsRemote(reqContext, s, project, name, instanceType)
	if err != nil {
		return err
	}

	if client == nil {
		return nil
	}

	forwarder := func() response.Response {
		return response.ForwardedResponse(client)
	}

	return response.NewRequestForwardRequiredError("", forwarder)
}
