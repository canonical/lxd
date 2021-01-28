package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/network/acl"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/version"
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
	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	recursion := util.IsRecursionRequest(r)

	// Get list of Network ACLs.
	aclNames, err := d.cluster.GetNetworkACLs(projectName)
	if err != nil {
		return response.InternalError(err)
	}

	resultString := []string{}
	resultMap := []api.NetworkACL{}
	for _, aclName := range aclNames {
		if !recursion {
			resultString = append(resultString, fmt.Sprintf("/%s/network-acls/%s", version.APIVersion, aclName))
		} else {
			netACL, err := acl.LoadByName(d.State(), projectName, aclName)
			if err != nil {
				continue
			}

			netACLInfo := netACL.Info()
			netACLInfo.UsedBy, _ = netACL.UsedBy() // Ignore errors in UsedBy, will return nil.

			resultMap = append(resultMap, *netACLInfo)
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

// Create Network ACL.
func networkACLsPost(d *Daemon, r *http.Request) response.Response {
	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	req := api.NetworkACLsPost{}

	// Parse the request into a record.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	_, err = acl.LoadByName(d.State(), projectName, req.Name)
	if err == nil {
		return response.BadRequest(fmt.Errorf("The network ACL already exists"))
	}

	err = acl.Create(d.State(), projectName, &req)
	if err != nil {
		return response.SmartError(err)
	}

	url := fmt.Sprintf("/%s/network-acls/%s", version.APIVersion, req.Name)
	return response.SyncResponseLocation(true, nil, url)
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
	projectName, _, err := project.NetworkProject(d.State().Cluster, projectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing Network ACL.
	netACL, err := acl.LoadByName(d.State(), projectName, mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	err = util.EtagCheck(r, netACL.Etag())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.NetworkACLPut{}

	// Decode the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = netACL.Update(&req)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// Rename Network ACL.
func networkACLPost(d *Daemon, r *http.Request) response.Response {
	return response.NotImplemented(nil)
}
