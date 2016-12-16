package main

import (
	"encoding/json"
	"io/ioutil"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/lxd/operation"
	"github.com/lxc/lxd/lxd/response"
)

type containerPostBody struct {
	Migration bool   `json:"migration"`
	Name      string `json:"name"`
}

func containerPost(d *Daemon, r *http.Request) response.Response {
	name := mux.Vars(r)["name"]
	c, err := containerLoadByName(d, name)
	if err != nil {
		return response.SmartError(err)
	}

	buf, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	body := containerPostBody{}
	if err := json.Unmarshal(buf, &body); err != nil {
		return response.BadRequest(err)
	}

	if body.Migration {
		ws, err := NewMigrationSource(c)
		if err != nil {
			return response.InternalError(err)
		}

		resources := map[string][]string{}
		resources["containers"] = []string{name}

		op, err := operation.Create(operation.ClassWebsocket, resources, ws.Metadata(), ws.Do, nil, ws.Connect)
		if err != nil {
			return response.InternalError(err)
		}

		return response.OperationResponse(op)
	}

	// Check that the name isn't already in use
	id, _ := dbContainerId(d.db, body.Name)
	if id > 0 {
		return response.Conflict
	}

	run := func(*operation.Operation) error {
		return c.Rename(body.Name)
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operation.Create(operation.ClassTask, resources, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return response.OperationResponse(op)
}
