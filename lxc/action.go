package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/lxc/config"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
)

// Start.
type cmdStart struct {
	global *cmdGlobal
	action *cmdAction
}

// The function  command() returns a cobra.Command object representing the "start" command.
// It is used to start one or more instances specified by the user.
func (c *cmdStart) command() *cobra.Command {
	cmdAction := cmdAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.Command("start")
	cmd.Use = usage("start", "[<remote>:]<instance> [[<remote>:]<instance>...]")
	cmd.Short = "Start instances"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpInstancesAction(toComplete, "start", c.action.flagForce)
	}

	return cmd
}

// Pause.
type cmdPause struct {
	global *cmdGlobal
	action *cmdAction
}

// The function  command() returns a cobra.Command object representing the "pause" command.
// It is used to pause (or freeze) one or more instances specified by the user. This command is hidden and has an alias "freeze".
func (c *cmdPause) command() *cobra.Command {
	cmdAction := cmdAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.Command("pause")
	cmd.Use = usage("pause", "[<remote>:]<instance> [[<remote>:]<instance>...]")
	cmd.Short = "Pause instances"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

The opposite of "lxc pause" is "lxc start".`)
	cmd.Aliases = []string{"freeze"}

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpInstancesAction(toComplete, "pause", c.action.flagForce)
	}

	return cmd
}

// Restart.
type cmdRestart struct {
	global *cmdGlobal
	action *cmdAction
}

// The function  command() returns a cobra.Command object representing the "restart" command.
// It is used to restart one or more instances specified by the user. This command restarts the instances, which is the opposite of the "pause" command.
func (c *cmdRestart) command() *cobra.Command {
	cmdAction := cmdAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.Command("restart")
	cmd.Use = usage("restart", "[<remote>:]<instance> [[<remote>:]<instance>...]")
	cmd.Short = "Restart instances"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpInstancesAction(toComplete, "restart", c.action.flagForce)
	}

	return cmd
}

// Stop.
type cmdStop struct {
	global *cmdGlobal
	action *cmdAction
}

// The function  command() returns a cobra.Command object representing the "stop" command.
// It is used to stop one or more instances specified by the user. This command stops the instances, effectively shutting them down.
func (c *cmdStop) command() *cobra.Command {
	cmdAction := cmdAction{global: c.global}
	c.action = &cmdAction

	cmd := c.action.Command("stop")
	cmd.Use = usage("stop", "[<remote>:]<instance> [[<remote>:]<instance>...]")
	cmd.Short = "Stop instances"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpInstancesAction(toComplete, "stop", c.action.flagForce)
	}

	return cmd
}

type cmdAction struct {
	global *cmdGlobal

	flagAll       bool
	flagConsole   string
	flagForce     bool
	flagStateful  bool
	flagStateless bool
	flagTimeout   int
}

// Command is a method of the cmdAction structure which constructs and configures a cobra Command object.
// It creates a command with a specific action, defines flags based on that action, and assigns appropriate help text.
func (c *cmdAction) Command(action string) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.RunE = c.run

	cmd.Flags().BoolVar(&c.flagAll, "all", false, "Run against all instances")

	switch action {
	case "stop":
		cmd.Flags().BoolVar(&c.flagStateful, "stateful", false, "Store the instance state")
	case "start":
		cmd.Flags().BoolVar(&c.flagStateless, "stateless", false, "Ignore the instance state")
	}

	if slices.Contains([]string{"start", "restart", "stop"}, action) {
		cmd.Flags().StringVar(&c.flagConsole, "console", "", cli.FormatStringFlagLabel("Immediately attach to the console"))
		cmd.Flags().Lookup("console").NoOptDefVal = "console"
	}

	if slices.Contains([]string{"restart", "stop"}, action) {
		cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, "Force the instance to stop")
		cmd.Flags().IntVar(&c.flagTimeout, "timeout", -1, cli.FormatStringFlagLabel("Time to wait for the instance to shutdown cleanly"))
	}

	return cmd
}

// doActionAll is a method of the cmdAction structure. It performs a specified action on all instances of a remote resource.
// It ensures that flags and parameters are appropriately set, and handles any errors that may occur during the process.
func (c *cmdAction) doActionAll(action string, resource remoteResource) error {
	if resource.name != "" {
		// both --all and instance name given.
		return errors.New("Both --all and instance name given")
	}

	remote := resource.remote
	d, err := c.global.conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	// Pause is called freeze.
	if action == "pause" {
		action = "freeze"
	}

	// Only store state if asked to.
	state := action == "stop" && c.flagStateful

	req := api.InstancesPut{
		State: &api.InstanceStatePut{
			Action:   action,
			Timeout:  c.flagTimeout,
			Force:    c.flagForce,
			Stateful: state,
		},
	}

	// Update all instances.
	op, err := d.UpdateInstances(req, "")
	if err != nil {
		return err
	}

	progress := cli.ProgressRenderer{
		Quiet: c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	return nil
}

// doAction is a method of the cmdAction structure. It carries out a specified action on an instance,
// using a given config and instance name. It manages state changes, flag checks, error handling and console attachment.
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

	if action == "stop" && c.flagForce && c.flagConsole != "" {
		return errors.New("--console can't be used while forcing instance shutdown")
	}

	remote, name, err := conf.ParseRemote(nameArg)
	if err != nil {
		return err
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	if name == "" {
		return fmt.Errorf("Must supply instance name for: %q", nameArg)
	}

	if action == "start" {
		current, _, err := d.GetInstance(name)
		if err != nil {
			return err
		}

		// "start" for a frozen instance means "unfreeze"
		if current.StatusCode == api.Frozen {
			action = "unfreeze"
		}

		// Always restore state (if present) unless asked not to
		if action == "start" && current.Stateful && !c.flagStateless {
			state = true
		}
	}

	req := api.InstanceStatePut{
		Action:   action,
		Timeout:  c.flagTimeout,
		Force:    c.flagForce,
		Stateful: state,
	}

	op, err := d.UpdateInstanceState(name, req, "")
	if err != nil {
		return err
	}

	if action == "stop" && c.flagConsole != "" {
		// Handle console attach
		console := cmdConsole{}
		console.global = c.global
		console.flagType = c.flagConsole
		return console.runConsole(d, name)
	}

	progress := cli.ProgressRenderer{
		Quiet: c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for operation to finish
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return fmt.Errorf("%s\nTry `lxc info --show-log %s` for more info", err, nameArg)
	}

	progress.Done("")

	// Handle console attach
	if c.flagConsole != "" {
		console := cmdConsole{}
		console.global = c.global
		console.flagType = c.flagConsole
		return console.runConsole(d, name)
	}

	return nil
}

// Run is a method of the cmdAction structure that implements the execution logic for the given Cobra command.
// It handles actions on instances (single or all) and manages error handling, console flag restrictions, and batch operations.
func (c *cmdAction) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	var names []string
	if c.flagAll {
		// If no server passed, use current default.
		if len(args) == 0 {
			args = []string{conf.DefaultRemote + ":"}
		}

		// Get all the servers.
		resources, err := c.global.ParseServers(args...)
		if err != nil {
			return err
		}

		for _, resource := range resources {
			// We don't allow instance names with --all.
			if resource.name != "" {
				return errors.New("Both --all and instance name given")
			}

			// See if we can use the bulk API.
			if resource.server.HasExtension("instance_bulk_state_change") {
				err = c.doActionAll(cmd.Name(), resource)
				if err != nil {
					return fmt.Errorf("%s: %w", resource.remote, err)
				}

				continue
			}

			ctslist, err := resource.server.GetInstances(api.InstanceTypeAny)
			if err != nil {
				return err
			}

			for _, ct := range ctslist {
				switch cmd.Name() {
				case "start":
					if ct.StatusCode == api.Running {
						continue
					}

				case "stop":
					if ct.StatusCode == api.Stopped {
						continue
					}
				}
				names = append(names, resource.remote+":"+ct.Name)
			}
		}
	} else {
		names = args

		if len(args) == 0 {
			_ = cmd.Usage()
			return nil
		}
	}

	if c.flagConsole != "" {
		if c.flagAll {
			return errors.New("--console can't be used with --all")
		}

		if len(names) != 1 {
			return errors.New("--console only works with a single instance")
		}
	}

	// Run the action for every listed instance
	results := runBatch(names, func(name string) error { return c.doAction(cmd.Name(), conf, name) })

	// Single instance is easy
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
		msg := fmt.Sprintf("error: %v", result.err)
		for line := range strings.SplitSeq(msg, "\n") {
			fmt.Fprintf(os.Stderr, "%s: %s\n", result.name, line)
		}
	}

	if !success {
		fmt.Fprintln(os.Stderr, "")
		return fmt.Errorf("Some instances failed to %s", cmd.Name())
	}

	return nil
}
