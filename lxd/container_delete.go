package main

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/operation"
	"github.com/lxc/lxd/lxd/response"
)

func containerDelete(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]
	c, err := containerLoadByName(d, name)
	if err != nil {
		return response.SmartError(err)
	}

	if c.IsRunning() {
		return response.BadRequest(fmt.Errorf("container is running"))
	}

	rmct := func(op *operation.Operation) error {
		return c.Delete()
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operation.Create(operation.ClassTask, resources, nil, rmct, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return response.OperationResponse(op)
}
