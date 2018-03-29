package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"syscall"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/shared"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type cmdConfigTemplate struct {
	global  *cmdGlobal
	config  *cmdConfig
	profile *cmdProfile
}

func (c *cmdConfigTemplate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("template")
	cmd.Short = i18n.G("Manage container file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage container file templates`))

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
	cmd.Use = i18n.G("create [<remote>:]<container> <template>")
	cmd.Short = i18n.G("Create new container file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create new container file templates`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTemplateCreate) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing container name"))
	}

	// Create container file template
	return resource.server.CreateContainerTemplateFile(resource.name, args[1], nil)
}

// Delete
type cmdConfigTemplateDelete struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate
}

func (c *cmdConfigTemplateDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("delete [<remote>:]<container> <template>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete container file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete container file templates`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTemplateDelete) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing container name"))
	}

	// Delete container file template
	return resource.server.DeleteContainerTemplateFile(resource.name, args[1])
}

// Edit
type cmdConfigTemplateEdit struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate
}

func (c *cmdConfigTemplateEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("edit [<remote>:]<container> <template>")
	cmd.Short = i18n.G("Edit container file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit container file templates`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTemplateEdit) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing container name"))
	}

	// Edit container file template
	if !termios.IsTerminal(int(syscall.Stdin)) {
		return resource.server.UpdateContainerTemplateFile(resource.name, args[1], os.Stdin)
	}

	reader, err := resource.server.GetContainerTemplateFile(resource.name, args[1])
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
		err := resource.server.UpdateContainerTemplateFile(resource.name, args[1], reader)
		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Error updating template file: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to start the editor again"))

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
}

func (c *cmdConfigTemplateList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("list [<remote>:]<container>")
	cmd.Short = i18n.G("List container file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List container file templates`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTemplateList) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing container name"))
	}

	// List the templates
	templates, err := resource.server.GetContainerTemplateFiles(resource.name)
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, template := range templates {
		data = append(data, []string{template})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{i18n.G("FILENAME")})
	sort.Sort(byName(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

// Show
type cmdConfigTemplateShow struct {
	global         *cmdGlobal
	config         *cmdConfig
	configTemplate *cmdConfigTemplate
}

func (c *cmdConfigTemplateShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("show [<remote>:]<container> <template>")
	cmd.Short = i18n.G("Show content of container file templates")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show content of container file templates`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTemplateShow) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
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
		return fmt.Errorf(i18n.G("Missing container name"))
	}

	// Show the template
	template, err := resource.server.GetContainerTemplateFile(resource.name, args[1])
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
