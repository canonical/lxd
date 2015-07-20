package main

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"
)

func removeContainer(d *Daemon, name string) error {
	if err := containerDeleteSnapshots(d, name); err != nil {
		return err
	}

	if err := d.Storage.ContainerDelete(name); err != nil {
		return err
	}

	if err := dbRemoveContainer(d, name); err != nil {
		return err
	}

	return nil
}

func containerDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	_, err := dbGetContainerID(d.db, name)
	if err != nil {
		return SmartError(err)
	}

	rmct := func() error {
		return removeContainer(d, name)
	}

	return AsyncResponse(shared.OperationWrap(rmct), nil)
}
