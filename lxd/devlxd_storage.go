package main

import (
	"net/http"

	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
)

var devLXDStoragePoolEndpoint = devLXDAPIEndpoint{
	Path: "/storage-pools/{poolName}",
	Get:  devLXDAPIEndpointAction{Handler: devLXDStoragePoolGetHandler},
}

func devLXDStoragePoolGetHandler(d *Daemon, r *http.Request) response.Response {
	inst, err := getInstanceFromContextAndCheckSecurityFlags(r.Context(), devLXDSecurityKey)
	if err != nil {
		return response.DevLXDErrorResponse(err, inst != nil && inst.Type() == instancetype.VM)
	}

	poolName := r.URL.Query().Get("poolName")
	target := request.QueryParam(r, "target")

	pool, etag, err := storagePoolGet(r.Context(), d.State(), poolName, inst.Project().Name, nil, target)
	if err != nil {
		return response.DevLXDErrorResponse(err, inst != nil && inst.Type() == instancetype.VM)
	}

	resp := api.DevLXDStoragePool{
		Name:   pool.Name,
		Driver: pool.Driver,
	}

	return response.DevLXDResponseETag(http.StatusOK, resp, etag, "json", inst.Type() == instancetype.VM)
}
