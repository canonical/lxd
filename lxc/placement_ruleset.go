package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/termios"
)

type cmdPlacementRuleset struct {
	global *cmdGlobal
}

func (c *cmdPlacementRuleset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("placement-ruleset")
	cmd.Short = i18n.G("Manage placement rulesets")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage placement rulesets"))

	// List.
	placementRulesetListCmd := cmdPlacementRulesetList{global: c.global, placementRuleset: c}
	cmd.AddCommand(placementRulesetListCmd.command())

	// Show.
	placementRulesetShowCmd := cmdPlacementRulesetShow{global: c.global, placementRuleset: c}
	cmd.AddCommand(placementRulesetShowCmd.command())

	// Create.
	placementRulesetCreateCmd := cmdPlacementRulesetCreate{global: c.global, placementRuleset: c}
	cmd.AddCommand(placementRulesetCreateCmd.command())

	// Edit.
	placementRulesetEditCmd := cmdPlacementRulesetEdit{global: c.global, placementRuleset: c}
	cmd.AddCommand(placementRulesetEditCmd.command())

	// Delete.
	placementRulesetDeleteCmd := cmdPlacementRulesetDelete{global: c.global, placementRuleset: c}
	cmd.AddCommand(placementRulesetDeleteCmd.command())

	// Rule
	placementRulesetRuleCmd := cmdPlacementRulesetRule{global: c.global, placementRuleset: c}
	cmd.AddCommand(placementRulesetRuleCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdPlacementRulesetList struct {
	global           *cmdGlobal
	placementRuleset *cmdPlacementRuleset

	flagFormat      string
	flagAllProjects bool
}

func (c *cmdPlacementRulesetList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available placement rulesets")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List available placement ruleset"))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Display placement rulesets from all projects"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdPlacementRulesetList) run(cmd *cobra.Command, args []string) error {
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

	// List the rulesets.
	if resource.name != "" {
		return errors.New(i18n.G("Filtering isn't supported yet"))
	}

	var rulesets []api.PlacementRuleset
	if c.flagAllProjects {
		rulesets, err = resource.server.GetPlacementRulesetsAllProjects()
		if err != nil {
			return err
		}
	} else {
		rulesets, err = resource.server.GetPlacementRulesets()
		if err != nil {
			return err
		}
	}

	data := [][]string{}
	for _, ruleset := range rulesets {
		strUsedBy := fmt.Sprint(len(ruleset.UsedBy))
		details := []string{
			ruleset.Name,
			ruleset.Description,
			strUsedBy,
		}

		if c.flagAllProjects {
			details = append([]string{ruleset.Project}, details...)
		}

		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("USED BY"),
	}

	if c.flagAllProjects {
		header = append([]string{i18n.G("PROJECT")}, header...)
	}

	return cli.RenderTable(c.flagFormat, header, data, rulesets)
}

// Show.
type cmdPlacementRulesetShow struct {
	global           *cmdGlobal
	placementRuleset *cmdPlacementRuleset
}

func (c *cmdPlacementRulesetShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<ruleset>"))
	cmd.Short = i18n.G("Show placement ruleset configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Show placement ruleset configurations"))
	cmd.RunE = c.run

	return cmd
}

func (c *cmdPlacementRulesetShow) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing placement ruleset name"))
	}

	// Show the placement ruleset config.
	ruleset, _, err := resource.server.GetPlacementRuleset(resource.name)
	if err != nil {
		return err
	}

	sort.Strings(ruleset.UsedBy)

	data, err := yaml.Marshal(&ruleset)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Create.
type cmdPlacementRulesetCreate struct {
	global           *cmdGlobal
	placementRuleset *cmdPlacementRuleset
}

func (c *cmdPlacementRulesetCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<ruleset> [key=value...]"))
	cmd.Short = i18n.G("Create new placement rulesets")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create new placement rulesets"))
	cmd.Example = cli.FormatSection("", i18n.G(`lxc placement ruleset create my-rule

lxc placement ruleset create my-rule < config.yaml
    Create placement ruleset my-rule with configuration from config.yaml`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdPlacementRulesetCreate) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
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
		return errors.New(i18n.G("Missing placement ruleset name"))
	}

	// If stdin isn't a terminal, read yaml from it.
	var rulesetPut api.PlacementRulesetPut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &rulesetPut)
		if err != nil {
			return err
		}
	}

	// Create the placement ruleset.
	ruleset := api.PlacementRulesetsPost{
		Name:                resource.name,
		PlacementRulesetPut: rulesetPut,
	}

	err = resource.server.CreatePlacementRuleset(ruleset)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Placement ruleset %s created")+"\n", resource.name)
	}

	return nil
}

// Edit.
type cmdPlacementRulesetEdit struct {
	global           *cmdGlobal
	placementRuleset *cmdPlacementRuleset
}

func (c *cmdPlacementRulesetEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<ruleset>"))
	cmd.Short = i18n.G("Edit placement ruleset configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Edit placement ruleset configurations as YAML"))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdPlacementRulesetEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the placement ruleset.
### Any line starting with a '# will be ignored.
###
### A placement ruleset consists of a description, a set of rules and a name.
### The name of the ruleset cannot be modified.
###
### An example would look like:
### name: my-ruleset
### description: Affinity for clusterGroup1
### placement_rules:
###   rule1:
###     required: true
###     priority: 0
###     kind: member-affinity
###     selector:
###       entity_type: cluster_group
###       matchers:
###         - property: name
###           values:
###             - clusterGroup1
`)
}

func (c *cmdPlacementRulesetEdit) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing placement ruleset name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `lxc placement ruleset show` command to be passed in here, but only take the contents
		// of the PlacementRulesetPut fields when updating the ruleset. The other fields are silently discarded.
		newdata := api.PlacementRuleset{}
		err = yaml.UnmarshalStrict(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdatePlacementRuleset(resource.name, newdata.Writable(), "")
	}

	// Get the current config.
	ruleset, etag, err := resource.server.GetPlacementRuleset(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&ruleset)
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
		newdata := api.PlacementRuleset{} // We show the full ruleset info, but only send the writable fields.
		err = yaml.UnmarshalStrict(content, &newdata)
		if err == nil {
			err = resource.server.UpdatePlacementRuleset(resource.name, newdata.Writable(), etag)
		}

		// Respawn the editor.
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

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

// Delete.
type cmdPlacementRulesetDelete struct {
	global           *cmdGlobal
	placementRuleset *cmdPlacementRuleset
}

func (c *cmdPlacementRulesetDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<ruleset>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete placement rulesets")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete placement rulesets"))
	cmd.RunE = c.run

	return cmd
}

func (c *cmdPlacementRulesetDelete) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing placement ruleset name"))
	}

	// Delete the placement ruleset.
	err = resource.server.DeletePlacementRuleset(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Placement ruleset %s deleted")+"\n", resource.name)
	}

	return nil
}

type cmdPlacementRulesetRule struct {
	global           *cmdGlobal
	placementRuleset *cmdPlacementRuleset
}

func (c *cmdPlacementRulesetRule) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rule")
	cmd.Short = i18n.G("Manage placement ruleset rules")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage placement ruleset rules"))

	// Add.
	placementRulesetRuleAddCmd := cmdPlacementRulesetRuleAdd{global: c.global, placementRulesetRule: c}
	cmd.AddCommand(placementRulesetRuleAddCmd.command())

	// Remove.
	placementRulesetRuleRemoveCmd := cmdPlacementRulesetRuleRemove{global: c.global, placementRulesetRule: c}
	cmd.AddCommand(placementRulesetRuleRemoveCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Add.
type cmdPlacementRulesetRuleAdd struct {
	global               *cmdGlobal
	placementRulesetRule *cmdPlacementRulesetRule
	flagPriority         int
}

func (c *cmdPlacementRulesetRuleAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<ruleset> <rule> [key=value...]"))
	cmd.Short = i18n.G("Create new placement ruleset rule")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create new placement ruleset rule"))
	cmd.Example = cli.FormatSection("", i18n.G(`lxc placement ruleset rule create my-ruleset my-rule instance member-anti-affinity config.user.foo=bar,baz

lxc placement ruleset rule create my-ruleset my-rule < config.yaml
    Create rule my-rule for ruleset my-ruleset with configuration from config.yaml`))

	cmd.Flags().IntVarP(&c.flagPriority, "priority", "p", -1, "Priority of the rule")
	cmd.RunE = c.run

	return cmd
}

func (c *cmdPlacementRulesetRuleAdd) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing placement ruleset name"))
	}

	ruleName := args[1]

	// If stdin isn't a terminal, read yaml from it.
	rule := &api.PlacementRule{}
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &rule)
		if err != nil {
			return err
		}
	}

	if len(args) > 2 {
		err := c.parseRule(rule, args[2:])
		if err != nil {
			return err
		}
	}

	ruleset, eTag, err := resource.server.GetPlacementRuleset(resource.name)
	if err != nil {
		return err
	}

	_, ok := ruleset.PlacementRules[ruleName]
	if ok {
		return fmt.Errorf(i18n.G("Placement rule %q already exists on ruleset %q"), ruleName, resource.name)
	}

	ruleset.PlacementRules[ruleName] = *rule
	err = resource.server.UpdatePlacementRuleset(resource.name, ruleset.Writable(), eTag)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Placement ruleset rule %s created")+"\n", args[1])
	}

	return nil
}

func (c *cmdPlacementRulesetRuleAdd) parseRule(rule *api.PlacementRule, args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("Instance placement rule requires a kind, an entity type, and at least one key value pair")
	}

	required := c.flagPriority < 0
	var priority int
	if !required {
		priority = c.flagPriority
	}

	rule.Required = required
	rule.Priority = priority
	rule.Kind = api.PlacementRuleKind(args[1])
	rule.Selector = api.Selector{
		EntityType: args[0],
	}

	property, values, ok := strings.Cut(args[2], "=")
	if !ok {
		return fmt.Errorf("Invalid selector matcher %q", args[2])
	}

	rule.Selector.Matchers = append(rule.Selector.Matchers, api.SelectorMatcher{
		Property: property,
		Values:   strings.Split(values, ","),
	})

	if len(args) > 3 {
		for _, arg := range args[3:] {
			property, values, ok := strings.Cut(arg, "=")
			if !ok {
				return fmt.Errorf("Invalid selector matcher %q", arg)
			}

			rule.Selector.Matchers = append(rule.Selector.Matchers, api.SelectorMatcher{
				Property: property,
				Values:   strings.Split(values, ","),
			})
		}
	}

	return nil
}

// Remove.
type cmdPlacementRulesetRuleRemove struct {
	global               *cmdGlobal
	placementRulesetRule *cmdPlacementRulesetRule
}

func (c *cmdPlacementRulesetRuleRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<ruleset> <rule>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete placement ruleset rule")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete placement ruleset rule"))
	cmd.RunE = c.run

	return cmd
}

func (c *cmdPlacementRulesetRuleRemove) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing placement ruleset name"))
	}

	// Delete the placement ruleset.
	ruleset, eTag, err := resource.server.GetPlacementRuleset(resource.name)
	if err != nil {
		return err
	}

	_, ok := ruleset.PlacementRules[args[1]]
	if !ok {
		return fmt.Errorf("Placement ruleset %q does not contain a rule with name %q", args[0], args[1])
	}

	delete(ruleset.PlacementRules, args[1])
	err = resource.server.UpdatePlacementRuleset(ruleset.Name, ruleset.Writable(), eTag)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Placement ruleset rule %s deleted")+"\n", args[1])
	}

	return nil
}
