package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
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

func (c *lxdContainer) TemplateApply(trigger string) error {
	fname := path.Join(c.PathGet(""), "metadata.yaml")

	if !shared.PathExists(fname) {
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

	for filepath, template := range metadata.Templates {
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

		fullpath := shared.VarPath("containers", c.name, "rootfs", strings.TrimLeft(filepath, "/"))

		if shared.PathExists(fullpath) {
			w, err = os.Create(fullpath)
			if err != nil {
				return err
			}
		} else {
			var uid, gid int
			if !c.IsPrivileged() {
				uid, gid := c.idmapset.ShiftIntoNs(0, 0)
				shared.MkdirAllOwner(path.Dir(fullpath), 0755, uid, gid)
			}

			w, err = os.Create(fullpath)
			if err != nil {
				return err
			}

			if !c.IsPrivileged() {
				w.Chown(uid, gid)
			}
			w.Chmod(0644)
		}

		tplString, err := ioutil.ReadFile(shared.VarPath("containers", c.name, "templates", template.Template))
		if err != nil {
			return err
		}

		tpl, err := pongo2.FromString("{% autoescape off %}" + string(tplString) + "{% endautoescape %}")
		if err != nil {
			return err
		}

		containerMeta := make(map[string]string)
		containerMeta["name"] = c.name
		containerMeta["architecture"], _ = shared.ArchitectureName(c.architecture)

		if c.ephemeral {
			containerMeta["ephemeral"] = "true"
		} else {
			containerMeta["ephemeral"] = "false"
		}

		if c.IsPrivileged() {
			containerMeta["privileged"] = "true"
		} else {
			containerMeta["privileged"] = "false"
		}

		configGet := func(confKey, confDefault *pongo2.Value) *pongo2.Value {
			val, ok := c.config[confKey.String()]
			if !ok {
				return confDefault
			}

			return pongo2.AsValue(strings.TrimRight(val, "\r\n"))
		}

		tpl.ExecuteWriter(pongo2.Context{"trigger": trigger,
			"path":       filepath,
			"container":  containerMeta,
			"config":     c.config,
			"devices":    c.devices,
			"properties": template.Properties,
			"config_get": configGet}, w)
	}

	return nil
}
