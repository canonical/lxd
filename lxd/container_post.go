package main

import (
	"encoding/json"
	"fmt"
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
		ws, err := migration.NewMigrationSource(c.c)
		if err != nil {
			return InternalError(err)
		}

		return AsyncResponseWithWs(ws, nil)
	}

	if c.c.Running() {
		return BadRequest(fmt.Errorf("renaming of running container not allowed"))
	}

	args := containerLXDArgs{
		Ctype:        cTypeRegular,
		Config:       c.config,
		Profiles:     c.profiles,
		Ephemeral:    c.ephemeral,
		BaseImage:    c.config["volatile.baseImage"],
		Architecture: c.architecture,
	}

	_, err = dbContainerCreate(d.db, body.Name, args)
	if err != nil {
		return SmartError(err)
	}

	run := func() error {
		oldPath := fmt.Sprintf("%s/", shared.VarPath("containers", c.name))
		newPath := fmt.Sprintf("%s/", shared.VarPath("containers", body.Name))

		if err := shared.FileMove(oldPath, newPath); err != nil {
			return err
		}

		if err = removeContainer(d, c); err != nil {
			return fmt.Errorf("error removing container after rename: %v", err)
		}
		return nil
	}

	return AsyncResponse(shared.OperationWrap(run), nil)
}
