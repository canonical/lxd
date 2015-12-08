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

	c, err := containerLoadById(d, id)
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

	target := r.FormValue("target")
	if target == "" {
		target = "unknown"
	}

	c, err := containerLoadById(d, id)
	if err != nil {
		return SmartError(err)
	}

	err = c.OnStop(target)
	if err != nil {
		return SmartError(err)
	}

	return EmptySyncResponse
}

var internalShutdownCmd = Command{name: "shutdown", put: internalShutdown}
var internalContainerOnStartCmd = Command{name: "containers/{id}/onstart", get: internalContainerOnStart}
var internalContainerOnStopCmd = Command{name: "containers/{id}/onstop", get: internalContainerOnStop}
