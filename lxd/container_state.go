package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/shared"
)

func containerStateGet(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]
	c, err := newLxdContainer(name, d)
	if err != nil {
		return SmartError(err)
	}

	state, err := c.RenderState()
	if err != nil {
		return InternalError(err)
	}

	return SyncResponse(true, state.Status)
}

func containerStatePut(d *Daemon, r *http.Request) Response {
	name := mux.Vars(r)["name"]

	raw := containerStatePutReq{}

	// We default to -1 (i.e. no timeout) here instead of 0 (instant
	// timeout).
	raw.Timeout = -1

	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return BadRequest(err)
	}

	c, err := newLxdContainer(name, d)
	if err != nil {
		return SmartError(err)
	}

	s, err := storageForContainer(d, c)
	if err != nil {
		return SmartError(fmt.Errorf("Couldn't detect storage: %v", err))
	}

	var do func() error
	switch shared.ContainerAction(raw.Action) {
	case shared.Start:
		do = func() error {
			if err = s.ContainerStart(c); err != nil {
				return err
			}
			if err = c.Start(); err != nil {
				return err
			}
			return nil
		}
	case shared.Stop:
		if raw.Timeout == 0 || raw.Force {
			do = func() error {
				if err = c.Stop(); err != nil {
					return err
				}
				if err = s.ContainerStop(c); err != nil {
					return err
				}
				return nil
			}
		} else {
			do = func() error {
				if err = c.Shutdown(time.Duration(raw.Timeout) * time.Second); err != nil {
					return err
				}
				if err = s.ContainerStop(c); err != nil {
					return err
				}
				return nil
			}
		}
	case shared.Restart:
		do = c.Reboot
	case shared.Freeze:
		do = c.Freeze
	case shared.Unfreeze:
		do = c.Unfreeze
	default:
		return BadRequest(fmt.Errorf("unknown action %s", raw.Action))
	}

	return AsyncResponse(shared.OperationWrap(do), nil)
}
