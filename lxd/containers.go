package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd"
)

func containersPost(d *Daemon, w http.ResponseWriter, r *http.Request) {
	lxd.Debugf("responding to create")

	if d.id_map == nil {
		BadRequest(w, fmt.Errorf("lxd's user has no subuids"))
		return
	}

	raw := lxd.Jmap{}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		BadRequest(w, err)
		return
	}

	name, err := raw.GetString("name")
	if err != nil {
		/* TODO: namegen code here */
		name = "foo"
	}

	source, err := raw.GetMap("source")
	if err != nil {
		BadRequest(w, err)
		return
	}

	type_, err := source.GetString("type")
	if err != nil {
		BadRequest(w, err)
		return
	}

	url, err := source.GetString("url")
	if err != nil {
		BadRequest(w, err)
		return
	}

	imageName, err := source.GetString("name")
	if err != nil {
		BadRequest(w, err)
		return
	}

	/* TODO: support other options here */
	if type_ != "remote" {
		NotImplemented(w)
		return
	}

	if url != "https+lxc-images://images.linuxcontainers.org" {
		NotImplemented(w)
		return
	}

	if imageName != "lxc-images/ubuntu/trusty/amd64" {
		NotImplemented(w)
		return
	}

	opts := lxc.TemplateOptions{
		Template: "download",
		Distro:   "ubuntu",
		Release:  "trusty",
		Arch:     "amd64",
	}

	c, err := lxc.NewContainer(name, d.lxcpath)
	if err != nil {
		return
	}

	/*
	 * Set the id mapping. This may not be how we want to do it, but it's a
	 * start.  First, we remove any id_map lines in the config which might
	 * have come from ~/.config/lxc/default.conf.  Then add id mapping based
	 * on Domain.id_map
	 */
	if d.id_map != nil {
		lxd.Debugf("setting custom idmap")
		err = c.SetConfigItem("lxc.id_map", "")
		if err != nil {
			fmt.Fprintf(w, "Failed to clear id mapping, continuing")
		}
		uidstr := fmt.Sprintf("u 0 %d %d\n", d.id_map.Uidmin, d.id_map.Uidrange)
		lxd.Debugf("uidstr is %s\n", uidstr)
		err = c.SetConfigItem("lxc.id_map", uidstr)
		if err != nil {
			fmt.Fprintf(w, "Failed to set uid mapping")
			return
		}
		gidstr := fmt.Sprintf("g 0 %d %d\n", d.id_map.Gidmin, d.id_map.Gidrange)
		err = c.SetConfigItem("lxc.id_map", gidstr)
		if err != nil {
			fmt.Fprintf(w, "Failed to set gid mapping")
			return
		}
		c.SaveConfigFile("/tmp/c")
	}

	/*
	 * Actually create the container
	 */
	AsyncResponse(func() error { return c.Create(opts) }, nil, w)
}

var containersCmd = Command{"containers", false, nil, nil, containersPost, nil}
