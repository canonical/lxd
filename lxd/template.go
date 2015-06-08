package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"gopkg.in/flosch/pongo2.v3"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
)

type TemplateEntry struct {
	When       []string
	Template   string
	Properties map[string]string
}

func templateApply(c *lxdContainer, trigger string) error {
	fname := shared.VarPath("lxc", c.name, "metadata.yaml")

	if _, err := os.Stat(fname); err != nil {
		return nil
	}

	content, err := ioutil.ReadFile(fname)
	if err != nil {
		return err
	}

	metadata := new(imageMetadata)
	err = yaml.Unmarshal(content, &metadata)

	if err != nil {
		return fmt.Errorf("Could not parse %s: %v", fname, err)
	}

	for path, template := range metadata.Templates {
		var w *os.File

		found := false
		for _, tplTrigger := range template.When {
			if tplTrigger == trigger {
				found = true
				break
			}
		}

		if !found {
			continue
		}

		fpath := shared.VarPath("lxc", c.name, "rootfs", strings.TrimLeft(path, "/"))

		if _, err := os.Stat(fpath); err == nil {
			w, err = os.Create(fpath)
			if err != nil {
				return err
			}
		} else {
			w, err = os.Create(fpath)
			if err != nil {
				return err
			}

			uid, gid := c.idmapset.ShiftIntoNs(0, 0)
			w.Chown(uid, gid)
			w.Chmod(0644)
		}

		tpl, err := pongo2.FromFile(shared.VarPath("lxc", c.name, "templates", template.Template))
		if err != nil {
			return err
		}

		container_meta := make(map[string]string)
		container_meta["name"] = c.name
		container_meta["architecture"], _ = shared.ArchitectureName(c.architecture)

		if c.ephemeral {
			container_meta["ephemeral"] = "true"
		} else {
			container_meta["ephemeral"] = "false"
		}

		if c.isPrivileged() {
			container_meta["privileged"] = "true"
		} else {
			container_meta["privileged"] = "false"
		}

		tpl.ExecuteWriter(pongo2.Context{"trigger": trigger,
			"path":       path,
			"container":  container_meta,
			"config":     c.config,
			"devices":    c.devices,
			"properties": template.Properties}, w)
	}

	return nil
}
