package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/shared/api"
)

var clusterCmd = Command{name: "cluster", post: clusterPost}

func clusterPost(d *Daemon, r *http.Request) Response {
	req := api.ClusterPost{}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return BadRequest(err)
	}

	// Sanity checks
	if req.Name == "" {
		return BadRequest(fmt.Errorf("No name provided"))
	}

	run := func(op *operation) error {
		return cluster.Bootstrap(d.State(), d.gateway, req.Name)
	}

	resources := map[string][]string{}
	resources["cluster"] = []string{}

	op, err := operationCreate(operationClassTask, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}
