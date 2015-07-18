package main

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"
)

func removeContainer(d *Daemon, container *lxdContainer) error {
	if err := containerDeleteSnapshots(d, container.name); err != nil {
		return err
	}

	if err := d.Storage.ContainerDelete(container); err != nil {
		return err
	}

	if err := dbRemoveContainer(d, container.name); err != nil {
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

	// TODO: i have added this not sure its a good idea (pcdummy)
	c, err := newLxdContainer(name, d)
	if err != nil {
		return SmartError(err)
	}

	rmct := func() error {
		return removeContainer(d, c)
	}

	return AsyncResponse(shared.OperationWrap(rmct), nil)
}
