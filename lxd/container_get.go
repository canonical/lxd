package main

import (
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"
)

func containerGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	//cId, err := dbGetContainerID(d.db, name)  will need cId to get info
	_, err := dbContainerIDGet(d.db, name)
	if err != nil {
		return NotFound
	}
	c, err := newLxdContainer(name, d)
	if err != nil {
		return SmartError(err)
	}

	state, err := c.RenderState()
	if err != nil {
		return InternalError(err)
	}

	targetPath := r.FormValue("log")
	if strings.ToLower(targetPath) == "true" || targetPath == "1" {
		fname := c.c.LogFile()

		f, err := os.Open(fname)
		if err != nil {
			return InternalError(err)
		}
		defer f.Close()

		log, err := shared.ReadLastNLines(f, 100)
		if err != nil {
			return InternalError(err)
		}
		state.Log = log
	}

	return SyncResponse(true, state)
}
