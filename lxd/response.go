package main

import (
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
		client, err := cluster.Connect(address, s.Endpoints.NetworkCert(), s.ServerCert(), r, false)
		if err != nil {
			return response.SmartError(err)
		}

		return response.ForwardedResponse(client, r)
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
	client, err := cluster.ConnectIfInstanceIsRemote(s, project, name, r, instanceType)
	if err != nil {
		return nil, err
	}

	if client == nil {
		return nil, nil
	}

	return response.ForwardedResponse(client, r), nil
}
