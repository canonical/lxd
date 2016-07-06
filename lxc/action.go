package main

import (
	"fmt"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type actionCmd struct {
	action         shared.ContainerAction
	hasTimeout     bool
	visible        bool
	name           string
	timeout        int
	force          bool
	stateful       bool
	stateless      bool
	additionalHelp string
}

func (c *actionCmd) showByDefault() bool {
	return c.visible
}

func (c *actionCmd) usage() string {
	if c.additionalHelp != "" {
		c.additionalHelp = fmt.Sprintf("\n\n%s", c.additionalHelp)
	}

	return fmt.Sprintf(i18n.G(
		`Changes state of one or more containers to %s.

lxc %s <name> [<name>...]%s`), c.name, c.name, c.additionalHelp)
}

func (c *actionCmd) flags() {
	if c.hasTimeout {
		gnuflag.IntVar(&c.timeout, "timeout", -1, i18n.G("Time to wait for the container before killing it."))
		gnuflag.BoolVar(&c.force, "f", false, i18n.G("Force the container to shutdown."))
		gnuflag.BoolVar(&c.force, "force", false, i18n.G("Force the container to shutdown."))
	}
	gnuflag.BoolVar(&c.stateful, "stateful", false, i18n.G("Store the container state (only for stop)."))
	gnuflag.BoolVar(&c.stateless, "stateless", false, i18n.G("Ignore the container state (only for start)."))
}

func (c *actionCmd) run(config *lxd.Config, args []string) error {
	if len(args) == 0 {
		return errArgs
	}

	state := false

	// Only store state if asked to
	if c.action == "stop" && c.stateful {
		state = true
	}

	for _, nameArg := range args {
		remote, name := config.ParseRemoteAndContainer(nameArg)
		d, err := lxd.NewClient(config, remote)
		if err != nil {
			return err
		}

		if name == "" {
			return fmt.Errorf(i18n.G("Must supply container name for: ")+"\"%s\"", nameArg)
		}

		if c.action == shared.Start {
			current, err := d.ContainerInfo(name)
			if err != nil {
				return err
			}

			// "start" for a frozen container means "unfreeze"
			if current.StatusCode == shared.Frozen {
				c.action = shared.Unfreeze
			}

			// Always restore state (if present) unless asked not to
			if c.action == shared.Start && current.Stateful && !c.stateless {
				state = true
			}
		}

		resp, err := d.Action(name, c.action, c.timeout, c.force, state)
		if err != nil {
			return err
		}

		if resp.Type != lxd.Async {
			return fmt.Errorf(i18n.G("bad result type from action"))
		}

		if err := d.WaitForSuccess(resp.Operation); err != nil {
			return fmt.Errorf("%s\n"+i18n.G("Try `lxc info --show-log %s` for more info"), err, nameArg)
		}
	}
	return nil
}
