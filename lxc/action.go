package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type actionCmd struct {
	action      shared.ContainerAction
	description string
	hasTimeout  bool
	visible     bool
	name        string
	timeout     int
	force       bool
	stateful    bool
	stateless   bool
	all         bool
}

func (c *actionCmd) showByDefault() bool {
	return c.visible
}

func (c *actionCmd) usage() string {
	extra := ""
	if c.name == "pause" {
		extra = "\n" + i18n.G("The opposite of \"lxc pause\" is \"lxc start\".")
	}

	return fmt.Sprintf(i18n.G(
		`Usage: lxc %s [--all] [<remote>:]<container> [[<remote>:]<container>...]

%s%s`), c.name, c.description, extra)
}

func (c *actionCmd) flags() {
	if c.hasTimeout {
		gnuflag.IntVar(&c.timeout, "timeout", -1, i18n.G("Time to wait for the container before killing it"))
		gnuflag.BoolVar(&c.force, "f", false, i18n.G("Force the container to shutdown"))
		gnuflag.BoolVar(&c.force, "force", false, i18n.G("Force the container to shutdown"))
	}
	gnuflag.BoolVar(&c.stateful, "stateful", false, i18n.G("Store the container state (only for stop)"))
	gnuflag.BoolVar(&c.stateless, "stateless", false, i18n.G("Ignore the container state (only for start)"))
	gnuflag.BoolVar(&c.all, "all", false, i18n.G("Run command against all containers"))
}

func (c *actionCmd) doAction(conf *config.Config, nameArg string) error {
	state := false

	// Only store state if asked to
	if c.action == "stop" && c.stateful {
		state = true
	}

	remote, name, err := conf.ParseRemote(nameArg)
	if err != nil {
		return err
	}

	d, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	if name == "" {
		return fmt.Errorf(i18n.G("Must supply container name for: ")+"\"%s\"", nameArg)
	}

	if c.action == shared.Start {
		current, _, err := d.GetContainer(name)
		if err != nil {
			return err
		}

		// "start" for a frozen container means "unfreeze"
		if current.StatusCode == api.Frozen {
			c.action = shared.Unfreeze
		}

		// Always restore state (if present) unless asked not to
		if c.action == shared.Start && current.Stateful && !c.stateless {
			state = true
		}
	}

	req := api.ContainerStatePut{
		Action:   string(c.action),
		Timeout:  c.timeout,
		Force:    c.force,
		Stateful: state,
	}

	op, err := d.UpdateContainerState(name, req, "")
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return fmt.Errorf("%s\n"+i18n.G("Try `lxc info --show-log %s` for more info"), err, nameArg)
	}

	return nil
}

func (c *actionCmd) run(conf *config.Config, args []string) error {
	var names []string
	if len(args) == 0 {
		if !c.all {
			return errArgs
		}
		d, err := conf.GetContainerServer(conf.DefaultRemote)
		if err != nil {
			return err
		}
		ctslist, err := d.GetContainers()
		if err != nil {
			return err
		}
		for _, ct := range ctslist {
			names = append(names, ct.Name)
		}
	} else {
		if c.all {
			return fmt.Errorf(i18n.G("Both --all and container name given"))
		}
		names = args
	}

	// Run the action for every listed container
	results := runBatch(names, func(name string) error { return c.doAction(conf, name) })

	// Single container is easy
	if len(results) == 1 {
		return results[0].err
	}

	// Do fancier rendering for batches
	success := true

	for _, result := range results {
		if result.err == nil {
			continue
		}

		success = false
		msg := fmt.Sprintf(i18n.G("error: %v"), result.err)
		for _, line := range strings.Split(msg, "\n") {
			fmt.Fprintln(os.Stderr, fmt.Sprintf("%s: %s", result.name, line))
		}
	}

	if !success {
		fmt.Fprintln(os.Stderr, "")
		return fmt.Errorf(i18n.G("Some containers failed to %s"), c.name)
	}

	return nil
}
