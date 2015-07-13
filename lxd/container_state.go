package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
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

func deactivateStorage(d *Daemon, c *lxdContainer) error {
	cpath := shared.VarPath("lxc", c.name)
	_, vgnameIsSet, err := getServerConfigValue(d, "core.lvm_vg_name")
	if err != nil {
		return fmt.Errorf("Error checking server config: %v", err)
	}

	if vgnameIsSet {
		output, err := exec.Command("umount", cpath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to unmount container path '%s'.\nError: %v\nOutput: %s", cpath, err, output)
		}
	}
	return nil
}

func activateStorage(d *Daemon, c *lxdContainer) error {
	cpath := shared.VarPath("lxc", c.name)
	vgname, vgnameIsSet, err := getServerConfigValue(d, "core.lvm_vg_name")
	if err != nil {
		return fmt.Errorf("Error checking server config: %v", err)
	}

	if vgnameIsSet {
		lvpath := fmt.Sprintf("/dev/%s/%s", vgname, c.name)
		output, err := exec.Command("mount", "-o", "discard", lvpath, cpath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("Error mounting snapshot LV: %v\noutput:'%s'", err, output)
		}
	}
	return nil
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

	var do func() error
	switch shared.ContainerAction(raw.Action) {
	case shared.Start:
		do = func() error {
			if err = activateStorage(d, c); err != nil {
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
				if err = deactivateStorage(d, c); err != nil {
					return err
				}
				return nil
			}
		} else {
			do = func() error {
				if err = c.Shutdown(time.Duration(raw.Timeout) * time.Second); err != nil {
					return err
				}
				if err = deactivateStorage(d, c); err != nil {
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
