package main

import (
	"encoding/json"
	"io/ioutil"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/shared"
)

func containerPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := newLxdContainer(name, d)
	if err != nil {
		return SmartError(err)
	}

	buf, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return InternalError(err)
	}

	body := containerPostBody{}
	if err := json.Unmarshal(buf, &body); err != nil {
		return BadRequest(err)
	}

	if body.Migration {
		lxc, err := c.LXContainerGet()
		if err != nil {
			return InternalError(err)
		}
		ws, err := migration.NewMigrationSource(lxc)
		if err != nil {
			return InternalError(err)
		}

		return AsyncResponseWithWs(ws, nil)
	}

	run := func() error {
		return c.Rename(body.Name)
	}

	return AsyncResponse(shared.OperationWrap(run), nil)
}
