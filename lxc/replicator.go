package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/termios"
)

type cmdReplicator struct {
	global *cmdGlobal
}

func (c *cmdReplicator) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("replicator")
	cmd.Short = "Manage replicators"
	cmd.Long = cli.FormatSection("Description", `Manage replicators`)

	// Create.
	replicatorCreateCmd := cmdReplicatorCreate{global: c.global}
	cmd.AddCommand(replicatorCreateCmd.command())

	// Delete.
	replicatorDeleteCmd := cmdReplicatorDelete{global: c.global}
	cmd.AddCommand(replicatorDeleteCmd.command())

	// Edit.
	replicatorEditCmd := cmdReplicatorEdit{global: c.global}
	cmd.AddCommand(replicatorEditCmd.command())

	// Get.
	replicatorGetCmd := cmdReplicatorGet{global: c.global}
	cmd.AddCommand(replicatorGetCmd.command())

	// Info.
	replicatorInfoCmd := cmdReplicatorInfo{global: c.global}
	cmd.AddCommand(replicatorInfoCmd.command())

	// List.
	replicatorListCmd := cmdReplicatorList{global: c.global}
	cmd.AddCommand(replicatorListCmd.command())

	// Rename.
	replicatorRenameCmd := cmdReplicatorRename{global: c.global}
	cmd.AddCommand(replicatorRenameCmd.command())

	// Run.
	replicatorRunCmd := cmdReplicatorRun{global: c.global}
	cmd.AddCommand(replicatorRunCmd.command())

	// Set.
	replicatorSetCmd := cmdReplicatorSet{global: c.global}
	cmd.AddCommand(replicatorSetCmd.command())

	// Show.
	replicatorShowCmd := cmdReplicatorShow{global: c.global}
	cmd.AddCommand(replicatorShowCmd.command())

	// Unset.
	replicatorUnsetCmd := cmdReplicatorUnset{global: c.global, replicatorSet: &replicatorSetCmd}
	cmd.AddCommand(replicatorUnsetCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdReplicatorCreate struct {
	global *cmdGlobal

	flagDescription string
}

func (c *cmdReplicatorCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", "[<remote>:]<replicator> [key=value...]")
	cmd.Short = "Create replicators"
	cmd.Long = cli.FormatSection("Description", `Create replicators

The "cluster" configuration key is required and must be set to the name of an existing cluster link.`)
	cmd.Example = cli.FormatSection("", `lxc replicator create my-replicator cluster=lxd_two
    Create a replicator called "my-replicator" targeting the cluster link "lxd_two".

lxc replicator create my-replicator cluster=lxd_two --project myproject
    Create a replicator in the project "myproject" targeting cluster link "lxd_two".

lxc replicator create my-replicator < config.yaml
    Create a replicator with the configuration from "config.yaml".`)
	cmd.Flags().StringVarP(&c.flagDescription, "description", "d", "", cli.FormatStringFlagLabel("Replicator description"))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdReplicatorCreate) run(cmd *cobra.Command, args []string) error {
	var stdinData api.ReplicatorPut

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// If stdin isn't a terminal, read text from it.
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Unmarshal(contents, &stdinData)
		if err != nil {
			return err
		}
	}

	// Parse remote.
	remoteName, replicatorName, err := c.global.conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	_, wrapper := newLocationHeaderTransportWrapper()
	client, err := c.global.conf.GetInstanceServerWithConnectionArgs(remoteName, &lxd.ConnectionArgs{TransportWrapper: wrapper})
	if err != nil {
		return err
	}

	replicator := api.ReplicatorsPost{
		Name:          replicatorName,
		ReplicatorPut: stdinData,
	}

	if c.flagDescription != "" {
		replicator.Description = c.flagDescription
	}

	if stdinData.Config == nil {
		replicator.Config = map[string]string{}
		for i := 1; i < len(args); i++ {
			entry := strings.SplitN(args[i], "=", 2)
			if len(entry) < 2 {
				return fmt.Errorf("Bad key=value pair: %s", args[i])
			}

			replicator.Config[entry[0]] = entry[1]
		}
	}

	err = client.CreateReplicator(c.global.flagProject, replicator)
	if err != nil {
		return err
	}

	return nil
}

// Delete.
type cmdReplicatorDelete struct {
	global *cmdGlobal
}

func (c *cmdReplicatorDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", "[<remote>:]<replicator>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = "Delete replicators"
	cmd.Long = cli.FormatSection("Description", `Delete replicators`)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("replicator", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdReplicatorDelete) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	err = resource.server.DeleteReplicator(c.global.flagProject, resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Replicator %s deleted\n", resource.name)
	}

	return nil
}

// Get.
type cmdReplicatorGet struct {
	global *cmdGlobal

	flagIsProperty bool
}

func (c *cmdReplicatorGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", "[<remote>:]<replicator> <key>")
	cmd.Short = "Get values for replicator configuration keys"
	cmd.Long = cli.FormatSection("Description", `Get values for replicator configuration keys`)

	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, cli.FormatStringFlagLabel("Get the key as a replicator property"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("replicator", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpReplicatorConfig(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdReplicatorGet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing replicator name")
	}

	replicator, _, err := resource.server.GetReplicator(c.global.flagProject, resource.name)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := replicator.Writable()
		res, err := getFieldByJSONTag(&w, args[1])
		if err != nil {
			return fmt.Errorf("The property %q does not exist on the replicator %q: %v", args[1], resource.name, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		fmt.Printf("%s\n", replicator.Config[args[1]])
	}

	return nil
}

// List.
type cmdReplicatorList struct {
	global     *cmdGlobal
	flagFormat string
}

func (c *cmdReplicatorList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", "[<remote>:]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = "List replicators"
	cmd.Long = cli.FormatSection("Description", `List replicators`)

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", "Format (csv|json|table|yaml|compact)")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, ":", true, instanceServerRemoteCompletionFilters(*c.global.conf)...)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdReplicatorList) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote.
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	replicators, err := resource.server.GetReplicators(c.global.flagProject)
	if err != nil {
		return err
	}

	const layout = "2006/01/02 15:04 MST"

	data := [][]string{}
	for _, replicator := range replicators {
		details := []string{
			replicator.Name,
			replicator.Description,
			replicator.Config["cluster"],
		}

		if shared.TimeIsSet(replicator.LastRunAt) {
			details = append(details, replicator.LastRunAt.Local().Format(layout))
		} else {
			details = append(details, "")
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		"NAME",
		"DESCRIPTION",
		"CLUSTER",
		"LAST RUN",
	}

	return cli.RenderTable(c.flagFormat, header, data, replicators)
}

// Run.
type cmdReplicatorRun struct {
	global *cmdGlobal

	flagRestore bool
}

func (c *cmdReplicatorRun) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("run", "[<remote>:]<replicator>")
	cmd.Short = "Run a replicator"
	cmd.Long = cli.FormatSection("Description", `Run a replicator

Runs the replicator, copying all instances in the source project to the target cluster.`)
	cmd.Example = cli.FormatSection("", `lxc replicator run my-replicator
    Run the replicator "my-replicator".

lxc replicator run my-replicator --restore
    Run the replicator "my-replicator" in restore mode, copying instances back from the target cluster.`)
	cmd.Flags().BoolVar(&c.flagRestore, "restore", false, "Restore instances from the target cluster back to the source project")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("replicator", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdReplicatorRun) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing replicator name")
	}

	action := "start"
	if c.flagRestore {
		action = "restore"
	}

	req := api.ReplicatorStatePut{
		Action: action,
	}

	op, err := resource.server.RunReplicator(c.global.flagProject, resource.name, req)
	if err != nil {
		return err
	}

	return op.Wait()
}

// Set.
type cmdReplicatorSet struct {
	global *cmdGlobal

	flagIsProperty bool
}

func (c *cmdReplicatorSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", "[<remote>:]<replicator> <key>=<value>...")
	cmd.Short = "Set replicator configuration keys"
	cmd.Long = cli.FormatSection("Description", `Set replicator configuration keys

For backward compatibility, a single configuration key may still be set with:
lxc replicator set [<remote>:]<replicator> <key> <value>`)

	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, cli.FormatStringFlagLabel("Set the key as a replicator property"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("replicator", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpReplicatorConfig(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdReplicatorSet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing replicator name")
	}

	replicator, etag, err := resource.server.GetReplicator(c.global.flagProject, resource.name)
	if err != nil {
		return err
	}

	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	writable := replicator.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJSONTag(&writable, k)
				if err != nil {
					return fmt.Errorf("Error unsetting property: %v", err)
				}
			}
		} else {
			err := unpackKVToWritable(&writable, keys)
			if err != nil {
				return fmt.Errorf("Error setting properties: %v", err)
			}
		}
	} else {
		maps.Copy(writable.Config, keys)
	}

	return resource.server.UpdateReplicator(c.global.flagProject, resource.name, writable, etag)
}

// Show.
type cmdReplicatorShow struct {
	global *cmdGlobal
}

func (c *cmdReplicatorShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", "[<remote>:]<replicator>")
	cmd.Short = "Show replicator configurations"
	cmd.Long = cli.FormatSection("Description", `Show replicator configurations`)
	cmd.Example = cli.FormatSection("", `lxc replicator show my-replicator
    Show the properties of the replicator "my-replicator".`)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("replicator", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdReplicatorShow) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing replicator name")
	}

	replicator, _, err := resource.server.GetReplicator(c.global.flagProject, resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&replicator)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Unset.
type cmdReplicatorUnset struct {
	global        *cmdGlobal
	replicatorSet *cmdReplicatorSet

	flagIsProperty bool
}

func (c *cmdReplicatorUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", "[<remote>:]<replicator> <key>")
	cmd.Short = "Unset replicator configuration keys"
	cmd.Long = cli.FormatSection("Description", `Unset replicator configuration keys`)

	cmd.RunE = c.run

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, cli.FormatStringFlagLabel("Unset the key as a replicator property"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("replicator", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpReplicatorConfig(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdReplicatorUnset) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.replicatorSet.flagIsProperty = c.flagIsProperty

	// Append empty value to satisfy set's minimum args requirement.
	args = append(args, "")
	return c.replicatorSet.run(cmd, args)
}

// Edit.
type cmdReplicatorEdit struct {
	global *cmdGlobal
}

func (c *cmdReplicatorEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", "[<remote>:]<replicator>")
	cmd.Short = "Edit replicator configurations as YAML"
	cmd.Long = cli.FormatSection("Description", cmd.Short)
	cmd.Example = cli.FormatSection("", `lxc replicator edit my-replicator
    Edit the replicator "my-replicator" in the default editor.

lxc replicator edit my-replicator < config.yaml
    Update the replicator "my-replicator" using the content of "config.yaml".`)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("replicator", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdReplicatorEdit) helpTemplate() string {
	return `### This is a YAML representation of the replicator.
### Any line starting with a '#' will be ignored.
###
### A replicator consists of a description and a configuration map.
###
### An example would look like:
### description: "Daily backup replicator"
### config:
###   cluster: lxd02
###   schedule: "@daily"
###   snapshot: "false"
###
### Note that the name is shown but cannot be changed`
}

func (c *cmdReplicatorEdit) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing replicator name")
	}

	// If stdin isn't a terminal, read text from it.
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ReplicatorPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateReplicator(c.global.flagProject, resource.name, newdata, "")
	}

	// Extract the current value.
	replicator, etag, err := resource.server.GetReplicator(c.global.flagProject, resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&replicator)
	if err != nil {
		return err
	}

	// Spawn the editor.
	content, err := shared.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor.
		newdata := api.ReplicatorPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateReplicator(c.global.flagProject, resource.name, newdata, etag)
		}

		// Respawn the editor.
		if err != nil {
			fmt.Fprintf(os.Stderr, "Config parsing error: %s\n", err)
			fmt.Println("Press enter to open the editor again or ctrl+c to abort change")

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = shared.TextEditor("", content)
			if err != nil {
				return err
			}

			continue
		}

		break
	}

	return nil
}

// Info.
type cmdReplicatorInfo struct {
	global *cmdGlobal
}

func (c *cmdReplicatorInfo) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", "[<remote>:]<replicator>")
	cmd.Short = "Show replicator state and job information"
	cmd.Long = cli.FormatSection("Description", `Show replicator state and job information

Displays the current state of the replicator including status, source project,
instances in the project, and child operation details when a run is in progress.`)
	cmd.Example = cli.FormatSection("", `lxc replicator info my-replicator
    Show the current state of the replicator "my-replicator".`)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("replicator", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdReplicatorInfo) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing replicator name")
	}

	replicator, _, err := resource.server.GetReplicator(c.global.flagProject, resource.name)
	if err != nil {
		return err
	}

	state, err := resource.server.GetReplicatorState(c.global.flagProject, resource.name)
	if err != nil {
		return err
	}

	instances, err := resource.server.GetInstances(lxd.GetInstancesArgs{})
	if err != nil {
		return err
	}

	instanceNames := make([]string, 0, len(instances))
	for _, inst := range instances {
		instanceNames = append(instanceNames, inst.Name)
	}

	sort.Strings(instanceNames)

	const layout = "2006/01/02 15:04 MST"

	fmt.Printf("Name: %s\n", replicator.Name)
	if replicator.Description != "" {
		fmt.Printf("Description: %s\n", replicator.Description)
	}

	fmt.Printf("Project: %s\n", replicator.Project)
	fmt.Printf("Status: %s\n", state.Status)

	schedule := replicator.Config["schedule"]
	if schedule != "" {
		fmt.Printf("Schedule: %s\n", schedule)
	}

	if shared.TimeIsSet(replicator.LastRunAt) {
		fmt.Printf("Last run: %s\n", replicator.LastRunAt.Local().Format(layout))
	}

	if schedule != "" {
		now := time.Now()
		var nextRun time.Time
		for _, s := range shared.SplitNTrimSpace(schedule, ",", -1, true) {
			sched, err := cron.ParseStandard(s)
			if err != nil {
				continue
			}

			next := sched.Next(now)
			if !next.IsZero() && (nextRun.IsZero() || next.Before(nextRun)) {
				nextRun = next
			}
		}

		if !nextRun.IsZero() {
			fmt.Printf("Next run: %s\n", nextRun.Local().Format(layout))
		}
	}

	// Render instances as a table.
	fmt.Println("Instances:")
	instanceData := make([][]string, 0, len(instanceNames))
	for _, name := range instanceNames {
		instanceData = append(instanceData, []string{name})
	}

	err = cli.RenderTable(cli.TableFormatTable, []string{"NAME"}, instanceData, instanceNames)
	if err != nil {
		return err
	}

	return nil
}

// Rename.
type cmdReplicatorRename struct {
	global *cmdGlobal
}

func (c *cmdReplicatorRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", "[<remote>:]<replicator> <new-name>")
	cmd.Aliases = []string{"mv"}
	cmd.Short = "Rename a replicator"
	cmd.Long = cli.FormatSection("Description", cmd.Short)

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("replicator", toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdReplicatorRename) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New("Missing replicator name")
	}

	err = resource.server.RenameReplicator(c.global.flagProject, resource.name, api.ReplicatorPost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf("Replicator %s renamed to %s\n", resource.name, args[1])
	}

	return nil
}
