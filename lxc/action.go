package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

// Start
type cmdStart struct {
	global *cmdGlobal
	action *cmdAction
}

func (c *cmdStart) Command() *cobra.Command {
	cmdAction := cmdAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.Command("start")
	cmd.Use = i18n.G("start [<remote>:]<container> [[<remote>:]<container>...]")
	cmd.Short = i18n.G("Start containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Start containers`))

	return cmd
}

// Pause
type cmdPause struct {
	global *cmdGlobal
	action *cmdAction
}

func (c *cmdPause) Command() *cobra.Command {
	cmdAction := cmdAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.Command("pause")
	cmd.Use = i18n.G("pause [<remote>:]<container> [[<remote>:]<container>...]")
	cmd.Short = i18n.G("Pause containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Pause containers`))
	cmd.Hidden = true

	return cmd
}

// Restart
type cmdRestart struct {
	global *cmdGlobal
	action *cmdAction
}

func (c *cmdRestart) Command() *cobra.Command {
	cmdAction := cmdAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.Command("restart")
	cmd.Use = i18n.G("restart [<remote>:]<container> [[<remote>:]<container>...]")
	cmd.Short = i18n.G("Restart containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Restart containers

The opposite of "lxc pause" is "lxc start".`))

	return cmd
}

// Stop
type cmdStop struct {
	global *cmdGlobal
	action *cmdAction
}

func (c *cmdStop) Command() *cobra.Command {
	cmdAction := cmdAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.Command("stop")
	cmd.Use = i18n.G("stop [<remote>:]<container> [[<remote>:]<container>...]")
	cmd.Short = i18n.G("Stop containers")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Stop containers`))

	return cmd
}

type cmdAction struct {
	global *cmdGlobal

	flagAll       bool
	flagForce     bool
	flagStateful  bool
	flagStateless bool
	flagTimeout   int
}

func (c *cmdAction) Command(action string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.RunE = c.Run

	cmd.Flags().BoolVar(&c.flagAll, "all", false, i18n.G("Run command against all containers"))

	if action == "stop" {
		cmd.Flags().BoolVar(&c.flagStateful, "stateful", false, i18n.G("Store the container state"))
	} else if action == "start" {
		cmd.Flags().BoolVar(&c.flagStateless, "stateless", false, i18n.G("Ignore the container state"))
	}

	if shared.StringInSlice(action, []string{"restart", "stop"}) {
		cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force the container to shutdown"))
		cmd.Flags().IntVar(&c.flagTimeout, "timeout", -1, i18n.G("Time to wait for the container before killing it")+"``")
	}

	return cmd
}

func (c *cmdAction) doAction(action string, conf *config.Config, nameArg string) error {
	state := false

	// Pause is called freeze
	if action == "pause" {
		action = "freeze"
	}

	// Only store state if asked to
	if action == "stop" && c.flagStateful {
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

	if action == "start" {
		current, _, err := d.GetContainer(name)
		if err != nil {
			return err
		}

		// "start" for a frozen container means "unfreeze"
		if current.StatusCode == api.Frozen {
			action = "unfreeze"
		}

		// Always restore state (if present) unless asked not to
		if action == "start" && current.Stateful && !c.flagStateless {
			state = true
		}
	}

	req := api.ContainerStatePut{
		Action:   action,
		Timeout:  c.flagTimeout,
		Force:    c.flagForce,
		Stateful: state,
	}

	op, err := d.UpdateContainerState(name, req, "")
	if err != nil {
		return err
	}

	progress := utils.ProgressRenderer{
		Quiet: c.global.flagQuiet,
	}
	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for operation to finish
	err = utils.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return fmt.Errorf("%s\n"+i18n.G("Try `lxc info --show-log %s` for more info"), err, nameArg)
	}

	progress.Done("")

	return nil
}

func (c *cmdAction) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	var names []string
	if len(args) == 0 {
		if !c.flagAll {
			cmd.Help()
			return nil
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
		if c.flagAll {
			return fmt.Errorf(i18n.G("Both --all and container name given"))
		}
		names = args
	}

	// Run the action for every listed container
	results := runBatch(names, func(name string) error { return c.doAction(cmd.Name(), conf, name) })

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
		return fmt.Errorf(i18n.G("Some containers failed to %s"), cmd.Name())
	}

	return nil
}
