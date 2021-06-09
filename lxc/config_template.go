package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type cmdConfigTemplate struct {
	global *cmdGlobal
	config *cmdConfig
}

func (c *cmdConfigTemplate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("template")
	cmd.Short = i18n.G("Manage instance file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage instance file templates`))

	// Create
	configTemplateCreateCmd := cmdConfigTemplateCreate{global: c.global, config: c.config, configTemplate: c}
	cmd.AddCommand(configTemplateCreateCmd.Command())

	// Delete
	configTemplateDeleteCmd := cmdConfigTemplateDelete{global: c.global, config: c.config, configTemplate: c}
	cmd.AddCommand(configTemplateDeleteCmd.Command())

	// Edit
	configTemplateEditCmd := cmdConfigTemplateEdit{global: c.global, config: c.config, configTemplate: c}
	cmd.AddCommand(configTemplateEditCmd.Command())

	// List
	configTemplateListCmd := cmdConfigTemplateList{global: c.global, config: c.config, configTemplate: c}
	cmd.AddCommand(configTemplateListCmd.Command())

	// Show
	configTemplateShowCmd := cmdConfigTemplateShow{global: c.global, config: c.config, configTemplate: c}
	cmd.AddCommand(configTemplateShowCmd.Command())

	return cmd
}

// Create
type cmdConfigTemplateCreate struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate
}

func (c *cmdConfigTemplateCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<instance> <template>"))
	cmd.Short = i18n.G("Create new instance file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create new instance file templates`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTemplateCreate) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
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
		return fmt.Errorf(i18n.G("Missing instance name"))
	}

	// Create instance file template
	return resource.server.CreateInstanceTemplateFile(resource.name, args[1], nil)
}

// Delete
type cmdConfigTemplateDelete struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate
}

func (c *cmdConfigTemplateDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<instance> <template>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete instance file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete instance file templates`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTemplateDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
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
		return fmt.Errorf(i18n.G("Missing instance name"))
	}

	// Delete instance file template
	return resource.server.DeleteInstanceTemplateFile(resource.name, args[1])
}

// Edit
type cmdConfigTemplateEdit struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate
}

func (c *cmdConfigTemplateEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<instance> <template>"))
	cmd.Short = i18n.G("Edit instance file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit instance file templates`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTemplateEdit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
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
		return fmt.Errorf(i18n.G("Missing instance name"))
	}

	// Edit instance file template
	if !termios.IsTerminal(getStdinFd()) {
		return resource.server.CreateInstanceTemplateFile(resource.name, args[1], os.Stdin)
	}

	reader, err := resource.server.GetInstanceTemplateFile(resource.name, args[1])
	if err != nil {
		return err
	}
	content, err := ioutil.ReadAll(reader)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err = shared.TextEditor("", content)
	if err != nil {
		return err
	}

	for {
		reader := bytes.NewReader(content)
		err := resource.server.CreateInstanceTemplateFile(resource.name, args[1], reader)
		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Error updating template file: %s")+"\n", err)
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

// List
type cmdConfigTemplateList struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate

	flagFormat string
}

func (c *cmdConfigTemplateList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]<instance>"))
	cmd.Short = i18n.G("List instance file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List instance file templates`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml)")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTemplateList) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing instance name"))
	}

	// List the templates
	templates, err := resource.server.GetInstanceTemplateFiles(resource.name)
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, template := range templates {
		data = append(data, []string{template})
	}
	sort.Sort(byName(data))

	header := []string{
		i18n.G("FILENAME"),
	}

	return utils.RenderTable(c.flagFormat, header, data, templates)
}

// Show
type cmdConfigTemplateShow struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate
}

func (c *cmdConfigTemplateShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<instance> <template>"))
	cmd.Short = i18n.G("Show content of instance file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show content of instance file templates`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTemplateShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
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
		return fmt.Errorf(i18n.G("Missing instance name"))
	}

	// Show the template
	template, err := resource.server.GetInstanceTemplateFile(resource.name, args[1])
	if err != nil {
		return err
	}

	content, err := ioutil.ReadAll(template)
	if err != nil {
		return err
	}

	fmt.Printf("%s", content)

	return nil
}
