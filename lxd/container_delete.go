package main

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"
)

func containerDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := containerLXDLoad(d, name)
	if err != nil {
		return SmartError(err)
	}

	if c.IsRunning() {
		return BadRequest(fmt.Errorf("container is running"))
	}

	rmct := func(id string) error {
		return c.Delete()
	}

	return AsyncResponse(shared.OperationWrap(rmct), nil)
}
