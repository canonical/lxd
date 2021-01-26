package main

import (
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/network/acl"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/response"
)

var networkACLsCmd = APIEndpoint{
	Path: "network-acls",

	Get:  APIEndpointAction{Handler: networkACLsGet, AccessHandler: allowProjectPermission("networks", "view")},
	Post: APIEndpointAction{Handler: networkACLsPost, AccessHandler: allowProjectPermission("networks", "manage-networks")},
}

var networkACLCmd = APIEndpoint{
	Path: "network-acls/{name}",

	Delete: APIEndpointAction{Handler: networkACLDelete, AccessHandler: allowProjectPermission("networks", "manage-networks")},
	Get:    APIEndpointAction{Handler: networkACLGet, AccessHandler: allowProjectPermission("networks", "view")},
	Put:    APIEndpointAction{Handler: networkACLPut, AccessHandler: allowProjectPermission("networks", "manage-networks")},
	Post:   APIEndpointAction{Handler: networkACLPost, AccessHandler: allowProjectPermission("networks", "manage-networks")},
}

// API endpoints.

// List Network ACLs.
func networkACLsGet(d *Daemon, r *http.Request) response.Response {
	return response.NotImplemented(nil)
}

// Create Network ACL.
func networkACLsPost(d *Daemon, r *http.Request) response.Response {
	return response.NotImplemented(nil)
}

// Delete Network ACL.
func networkACLDelete(d *Daemon, r *http.Request) response.Response {
	return response.NotImplemented(nil)
}

// Show Network ACL.
func networkACLGet(d *Daemon, r *http.Request) response.Response {
	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	netACL, err := acl.LoadByName(d.State(), projectName, mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	info := netACL.Info()
	info.UsedBy, err = netACL.UsedBy()
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, info, netACL.Etag())
}

// Update Network ACL.
func networkACLPut(d *Daemon, r *http.Request) response.Response {
	return response.NotImplemented(nil)
}

// Rename Network ACL.
func networkACLPost(d *Daemon, r *http.Request) response.Response {
	return response.NotImplemented(nil)
}
