package main

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
)

func containerDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := containerLoadByName(d, name)
	if err != nil {
		return SmartError(err)
	}

	if c.IsRunning() {
		return BadRequest(fmt.Errorf("container is running"))
	}

	rmct := func(op *operation) error {
		return c.Delete()
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operationCreate(operationClassTask, resources, nil, rmct, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}
