package main

import (
	"fmt"
	"net/http"

	"gopkg.in/lxc/go-lxc.v2"

	"github.com/lxc/lxd"
)

type byname func(*lxc.Container) error

func buildByNameServe(function string, f byname, d *Daemon) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		lxd.Debugf("responding to %s", function)

		if d.id_map == nil {
			BadRequest(w, fmt.Errorf("lxd's user has no subuids"))
			return
		}

		if !d.isTrustedClient(r) {
			lxd.Debugf("%s request from untrusted client", function)
			return
		}

		name := r.FormValue("name")
		if name == "" {
			fmt.Fprintf(w, "failed parsing name")
			return
		}

		c, err := lxc.NewContainer(name, d.lxcpath)
		if err != nil {
			fmt.Fprintf(w, "failed getting container")
			return
		}

		err = f(c)
		if err != nil {
			fmt.Fprintf(w, "operation failed")
			return
		}
	}
}
