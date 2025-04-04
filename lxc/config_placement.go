package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdConfigPlacement struct {
	global       *cmdGlobal
	config       *cmdConfig
	profile      *cmdProfile
	flagPriority int
}

func (c *cmdConfigPlacement) parseRule(args []string) (*api.InstancePlacementRule, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("Instance placement rule requires a kind, an entity type, and at least one key value pair")
	}

	required := c.flagPriority < 0
	var priority int
	if !required {
		priority = c.flagPriority
	}

	rule := api.InstancePlacementRule{
		Required: required,
		Kind:     api.InstancePlacementRuleKind(args[0]),
		Priority: priority,
		Selector: api.Selector{
			EntityType: args[1],
			Matchers:   nil,
		},
	}

	proprty, values, ok := strings.Cut(args[2], "=")
	if !ok {
		return nil, fmt.Errorf("Invalid selector matcher %q", args[2])
	}

	rule.Selector.Matchers = append(rule.Selector.Matchers, api.SelectorMatcher{
		Property: proprty,
		Values:   strings.Split(values, ","),
	})

	if len(args) > 3 {
		for _, arg := range args[3:] {
			proprty, values, ok := strings.Cut(arg, "=")
			if !ok {
				return nil, fmt.Errorf("Invalid selector matcher %q", arg)
			}

			rule.Selector.Matchers = append(rule.Selector.Matchers, api.SelectorMatcher{
				Property: proprty,
				Values:   strings.Split(values, ","),
			})
		}
	}

	return &rule, nil
}

func (c *cmdConfigPlacement) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("placement")
	cmd.Short = i18n.G("Manage placement rules")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage placement rules`))

	// Add
	configPlacementAddCmd := cmdConfigPlacementAdd{global: c.global, config: c.config, profile: c.profile, configPlacement: c}
	cmd.AddCommand(configPlacementAddCmd.command())

	// Override
	if c.config != nil {
		configPlacementOverrideCmd := cmdConfigPlacementOverride{global: c.global, config: c.config, profile: c.profile, configPlacement: c}
		cmd.AddCommand(configPlacementOverrideCmd.command())
	}

	// Remove
	configPlacementRemoveCmd := cmdConfigPlacementRemove{global: c.global, config: c.config, profile: c.profile, configPlacement: c}
	cmd.AddCommand(configPlacementRemoveCmd.command())

	// Show
	configPlacementShowCmd := cmdConfigPlacementShow{global: c.global, config: c.config, profile: c.profile}
	cmd.AddCommand(configPlacementShowCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Add.
type cmdConfigPlacementAdd struct {
	global          *cmdGlobal
	config          *cmdConfig
	configPlacement *cmdConfigPlacement
	profile         *cmdProfile
}

func (c *cmdConfigPlacementAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Short = i18n.G("Add instance placement rules")
	cmd.Flags().IntVar(&c.configPlacement.flagPriority, "priority", -1, "The instance placement rule priority")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add instance placement rules`))
	if c.config != nil {
		cmd.Use = usage("add", i18n.G("[<remote>:]<instance> <rule-name> <kind> <entity-type> <key>=<value> [key=value...] [--priority <priority>]"))
		cmd.Example = cli.FormatSection("", i18n.G(
			`lxc config placement add [<remote>:]instance1 <rule-name> affinity cluster_group name=gpu
    Will configure an affinity rule for the instance with name "instance1" for the cluster group with name "gpu".

lxc config placement add [<remote>:]instance1 <rule-name> anti-affinity instance config.user.foo=bar,baz
	Will configure an anti-affinity rule for the instance with name "instance1" against other instances whose value for "config.user.foo" is "bar" or "baz".`))
	} else if c.profile != nil {
		cmd.Use = usage("add", i18n.G("[<remote>:]<profile> <rule-name> <kind> <entity-type> <key>=<value> [key=value...] [--priority <priority>]"))
		cmd.Example = cli.FormatSection("", i18n.G(
			`lxc profile placement add [<remote>:]profile1 <rule-name> affinity cluster_group name=gpu
    Will configure an affinity rule for the instances with profile "profile1" for the cluster group with name "gpu".

lxc profile placement add [<remote>:]profile1 <rule-name> anti-affinity instance config.user.foo=bar,baz
	Will configure an anti-affinity rule for instances with profile "profile1" against other instances whose value for "config.user.foo" is "bar" or "baz".`))
	}

	cmd.RunE = c.run
	return cmd
}

func (c *cmdConfigPlacementAdd) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 5, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing name"))
	}

	// Add the rule
	rulename := args[1]

	rule, err := c.configPlacement.parseRule(args[2:])
	if err != nil {
		return err
	}

	if c.profile != nil {
		profile, etag, err := resource.server.GetProfile(resource.name)
		if err != nil {
			return err
		}

		if profile.PlacementRules == nil {
			profile.PlacementRules = make(map[string]api.InstancePlacementRule)
		}

		_, ok := profile.PlacementRules[rulename]
		if ok {
			return errors.New(i18n.G("The rule already exists"))
		}

		profile.PlacementRules[rulename] = *rule

		err = resource.server.UpdateProfile(resource.name, profile.Writable(), etag)
		if err != nil {
			return err
		}
	} else {
		inst, etag, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return err
		}

		if inst.PlacementRules == nil {
			inst.PlacementRules = make(map[string]api.InstancePlacementRule)
		}

		_, ok := inst.PlacementRules[rulename]
		if ok {
			return errors.New(i18n.G("The rule already exists"))
		}

		inst.PlacementRules[rulename] = *rule

		op, err := resource.server.UpdateInstance(resource.name, inst.Writable(), etag)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Placement rule %s added to %s")+"\n", rulename, resource.name)
	}

	return nil
}

// Override.
type cmdConfigPlacementOverride struct {
	global          *cmdGlobal
	config          *cmdConfig
	configPlacement *cmdConfigPlacement
	profile         *cmdProfile
}

func (c *cmdConfigPlacementOverride) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("override", i18n.G("[<remote>:]<instance> <rule-name> <kind> [<entity-type>] [key=value...] [--priority <priority>]"))
	cmd.Short = i18n.G("Override profile inherited placement rules")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Override profile inherited placement rules`))
	cmd.Flags().IntVar(&c.configPlacement.flagPriority, "priority", -1, "The instance placement rule priority")

	cmd.RunE = c.run

	return cmd
}

func (c *cmdConfigPlacementOverride) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing name"))
	}

	// Override the rule
	inst, etag, err := resource.server.GetInstance(resource.name)
	if err != nil {
		return err
	}

	rulename := args[1]
	_, ok := inst.PlacementRules[rulename]
	if ok {
		return errors.New(i18n.G("The rule already exists"))
	}

	_, ok = inst.ExpandedPlacementRules[rulename]
	if !ok {
		return errors.New(i18n.G("The profile placement doesn't exist"))
	}

	var rule api.InstancePlacementRule
	kind := args[2]
	if len(args) > 3 {
		r, err := c.configPlacement.parseRule(args[3:])
		if err != nil {
			return err
		}

		rule = *r
	} else {
		rule = api.InstancePlacementRule{Kind: api.InstancePlacementRuleKind(kind)}
	}

	inst.PlacementRules[rulename] = rule

	op, err := resource.server.UpdateInstance(resource.name, inst.Writable(), etag)
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Placement %s overridden for %s")+"\n", rulename, resource.name)
	}

	return nil
}

// Remove.
type cmdConfigPlacementRemove struct {
	global          *cmdGlobal
	config          *cmdConfig
	configPlacement *cmdConfigPlacement
	profile         *cmdProfile
}

func (c *cmdConfigPlacementRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	if c.config != nil {
		cmd.Use = usage("remove", i18n.G("[<remote>:]<instance> <name>..."))
	} else if c.profile != nil {
		cmd.Use = usage("remove", i18n.G("[<remote>:]<profile> <name>..."))
	}

	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Remove instance placement rules")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove instance placement rules`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdConfigPlacementRemove) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing name"))
	}

	// Remove the rule
	if c.profile != nil {
		profile, etag, err := resource.server.GetProfile(resource.name)
		if err != nil {
			return err
		}

		for _, rulename := range args[1:] {
			_, ok := profile.PlacementRules[rulename]
			if !ok {
				return errors.New(i18n.G("Placement rule doesn't exist"))
			}

			delete(profile.PlacementRules, rulename)
		}

		err = resource.server.UpdateProfile(resource.name, profile.Writable(), etag)
		if err != nil {
			return err
		}
	} else {
		inst, etag, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return err
		}

		for _, rulename := range args[1:] {
			_, ok := inst.PlacementRules[rulename]
			if !ok {
				_, ok := inst.PlacementRules[rulename]
				if !ok {
					return errors.New(i18n.G("Placement rule doesn't exist"))
				}

				return errors.New(i18n.G("Placement rule from profile(s) cannot be removed from individual instance. Override rule or modify profile instead"))
			}

			delete(inst.PlacementRules, rulename)
		}

		op, err := resource.server.UpdateInstance(resource.name, inst.Writable(), etag)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Placement %s removed from %s")+"\n", strings.Join(args[1:], ", "), resource.name)
	}

	return nil
}

// Show.
type cmdConfigPlacementShow struct {
	global  *cmdGlobal
	config  *cmdConfig
	profile *cmdProfile
}

func (c *cmdConfigPlacementShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	if c.config != nil {
		cmd.Use = usage("show", i18n.G("[<remote>:]<instance>"))
	} else if c.profile != nil {
		cmd.Use = usage("show", i18n.G("[<remote>:]<profile>"))
	}

	cmd.Short = i18n.G("Show full placement rule configuration")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show full placement rule configuration`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdConfigPlacementShow) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing name"))
	}

	// Show the placement rules
	var placementRules map[string]api.InstancePlacementRule
	if c.profile != nil {
		profile, _, err := resource.server.GetProfile(resource.name)
		if err != nil {
			return err
		}

		placementRules = profile.PlacementRules
	} else {
		inst, _, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return err
		}

		placementRules = inst.PlacementRules
	}

	data, err := yaml.Marshal(&placementRules)
	if err != nil {
		return err
	}

	fmt.Print(string(data))

	return nil
}
