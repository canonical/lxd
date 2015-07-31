package main

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"

	log "gopkg.in/inconshreveable/log15.v2"
)

func removeContainer(d *Daemon, container *lxdContainer) error {
	if err := containerDeleteSnapshots(d, container.name); err != nil {
		return err
	}

	s, err := storageForContainer(d, container)
	if err != nil {
		shared.Log.Warn("Couldn't detect storage.", log.Ctx{"container": container})
	} else {
		if err := s.ContainerDelete(container); err != nil {
			shared.Log.Warn("Couldn't delete container storage.", log.Ctx{"container": container, "storage": s})
		}
	}

	if err := dbContainerRemove(d.db, container.name); err != nil {
		return err
	}

	return nil
}

func containerDelete(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := newLxdContainer(name, d)
	if err != nil {
		return SmartError(err)
	}

	rmct := func() error {
		return removeContainer(d, c)
	}

	return AsyncResponse(shared.OperationWrap(rmct), nil)
}
