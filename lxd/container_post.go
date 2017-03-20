package main

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func containerPost(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := containerLoadByName(d, name)
	if err != nil {
		return SmartError(err)
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return InternalError(err)
	}

	rdr1 := ioutil.NopCloser(bytes.NewBuffer(body))
	rdr2 := ioutil.NopCloser(bytes.NewBuffer(body))

	reqRaw := shared.Jmap{}
	if err := json.NewDecoder(rdr1).Decode(&reqRaw); err != nil {
		return BadRequest(err)
	}

	req := api.ContainerPost{}
	if err := json.NewDecoder(rdr2).Decode(&req); err != nil {
		return BadRequest(err)
	}

	// Check if stateful (backward compatibility)
	stateful := true
	_, err = reqRaw.GetBool("live")
	if err == nil {
		stateful = req.Live
	}

	if req.Migration {
		ws, err := NewMigrationSource(c, stateful)
		if err != nil {
			return InternalError(err)
		}

		resources := map[string][]string{}
		resources["containers"] = []string{name}

		op, err := operationCreate(operationClassWebsocket, resources, ws.Metadata(), ws.Do, nil, ws.Connect)
		if err != nil {
			return InternalError(err)
		}

		return OperationResponse(op)
	}

	// Check that the name isn't already in use
	id, _ := dbContainerId(d.db, req.Name)
	if id > 0 {
		return Conflict
	}

	run := func(*operation) error {
		return c.Rename(req.Name)
	}

	resources := map[string][]string{}
	resources["containers"] = []string{name}

	op, err := operationCreate(operationClassTask, resources, nil, run, nil, nil)
	if err != nil {
		return InternalError(err)
	}

	return OperationResponse(op)
}
