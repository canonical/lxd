package main

import (
	"fmt"
	"net/http"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd"
)

func (d *Daemon) serveCreate(w http.ResponseWriter, r *http.Request) {
	lxd.Debugf("responding to create")

	name := r.FormValue("name")
	if name == "" {
		fmt.Fprintf(w, "failed parsing name")
		return
	}

	distro := r.FormValue("distro")
	if distro == "" {
		fmt.Fprintf(w, "failed parsing distro")
		return
	}

	release := r.FormValue("release")
	if release == "" {
		fmt.Fprintf(w, "failed parsing release")
		return
	}

	arch := r.FormValue("arch")
	if arch == "" {
		fmt.Fprintf(w, "failed parsing arch")
		return
	}

	opts := lxc.TemplateOptions{
		Template: "download",
		Distro:   distro,
		Release:  release,
		Arch:     arch,
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
	err = c.Create(opts)
	if err != nil {
		fmt.Fprintf(w, "fail!")
	} else {
		fmt.Fprintf(w, "success!")
	}
}
