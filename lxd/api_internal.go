package main

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

var apiInternal = []Command{
	internalShutdownCmd,
	internalContainerOnStartCmd,
	internalContainerOnStopCmd,
}

func internalShutdown(d *Daemon, r *http.Request) Response {
	d.shutdownChan <- true

	return EmptySyncResponse
}

func internalContainerOnStart(d *Daemon, r *http.Request) Response {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		return SmartError(err)
	}

	name, err := dbContainerName(d.db, id)
	if err != nil {
		return SmartError(err)
	}

	c, err := containerLXDLoad(d, name)
	if err != nil {
		return SmartError(err)
	}

	err = c.OnStart()
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

func internalContainerOnStop(d *Daemon, r *http.Request) Response {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		return SmartError(err)
	}

	name, err := dbContainerName(d.db, id)
	if err != nil {
		return SmartError(err)
	}

	c, err := containerLXDLoad(d, name)
	if err != nil {
		return SmartError(err)
	}

	err = c.OnStop()
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

var internalShutdownCmd = Command{name: "shutdown", put: internalShutdown}
var internalContainerOnStartCmd = Command{name: "containers/{id}/onstart", get: internalContainerOnStart}
var internalContainerOnStopCmd = Command{name: "containers/{id}/onstop", get: internalContainerOnStop}
