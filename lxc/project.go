package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"syscall"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type cmdProject struct {
	global *cmdGlobal
}

func (c *cmdProject) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("project")
	cmd.Short = i18n.G("Manage projects")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage projects`))

	// Create
	projectCreateCmd := cmdProjectCreate{global: c.global, project: c}
	cmd.AddCommand(projectCreateCmd.Command())

	// Delete
	projectDeleteCmd := cmdProjectDelete{global: c.global, project: c}
	cmd.AddCommand(projectDeleteCmd.Command())

	// Edit
	projectEditCmd := cmdProjectEdit{global: c.global, project: c}
	cmd.AddCommand(projectEditCmd.Command())

	// List
	projectListCmd := cmdProjectList{global: c.global, project: c}
	cmd.AddCommand(projectListCmd.Command())

	// Rename
	projectRenameCmd := cmdProjectRename{global: c.global, project: c}
	cmd.AddCommand(projectRenameCmd.Command())

	// Show
	projectShowCmd := cmdProjectShow{global: c.global, project: c}
	cmd.AddCommand(projectShowCmd.Command())

	// Set default
	projectSwitchCmd := cmdProjectSwitch{global: c.global, project: c}
	cmd.AddCommand(projectSwitchCmd.Command())

	return cmd
}

// Create
type cmdProjectCreate struct {
	global  *cmdGlobal
	project *cmdProject
}

func (c *cmdProjectCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("create [<remote>:]<project>")
	cmd.Short = i18n.G("Create projects")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create projects`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProjectCreate) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing project name"))
	}

	// Create the project
	project := api.ProjectsPost{}
	project.Name = resource.name

	err = resource.server.CreateProject(project)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Project %s created")+"\n", resource.name)
	}

	return nil
}

// Delete
type cmdProjectDelete struct {
	global  *cmdGlobal
	project *cmdProject
}

func (c *cmdProjectDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("delete [<remote>:]<project>")
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete projects")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete projects`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProjectDelete) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing project name"))
	}

	// Delete the project
	err = resource.server.DeleteProject(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Project %s deleted")+"\n", resource.name)
	}

	return nil
}

// Edit
type cmdProjectEdit struct {
	global  *cmdGlobal
	project *cmdProject
}

func (c *cmdProjectEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("edit [<remote>:]<project>")
	cmd.Short = i18n.G("Edit project configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit project configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc project edit <project> < project.yaml
    Update a project using the content of project.yaml`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProjectEdit) helpTemplate() string {
	return i18n.G(
		`### This is a yaml representation of the project.
### Any line starting with a '# will be ignored.
###
### A project consists of a set of features and a description.
###
### An example would look like:
### name: my-project
### features:
###   images: True
###   profiles: True
### description: My own project
###
### Note that the name is shown but cannot be changed`)
}

func (c *cmdProjectEdit) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing project name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.ProjectPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateProject(resource.name, newdata, "")
	}

	// Extract the current value
	project, etag, err := resource.server.GetProject(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&project)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.ProjectPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateProject(resource.name, newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again"))

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
type cmdProjectList struct {
	global  *cmdGlobal
	project *cmdProject
}

func (c *cmdProjectList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("list [<remote>:]")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List projects")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List projects`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProjectList) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := conf.DefaultRemote
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// List projects
	projects, err := resource.server.GetProjects()
	if err != nil {
		return err
	}

	fmt.Printf("%s: %v %s\n", remote, conf.Remotes[remote], conf.Remotes[remote].Project)
	currentProject := conf.Remotes[remote].Project
	if currentProject == "" {
		currentProject = "default"
	}

	data := [][]string{}
	for _, project := range projects {
		images := i18n.G("NO")
		if project.Features.Images {
			images = i18n.G("YES")
		}

		profiles := i18n.G("NO")
		if project.Features.Profiles {
			profiles = i18n.G("YES")
		}

		name := project.Name
		if name == currentProject {
			name = fmt.Sprintf("%s (%s)", name, i18n.G("current"))
		}

		strUsedBy := fmt.Sprintf("%d", len(project.UsedBy))
		data = append(data, []string{name, images, profiles, strUsedBy})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("NAME"),
		i18n.G("IMAGES"),
		i18n.G("PROFILES"),
		i18n.G("USED BY")})
	sort.Sort(byName(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

// Rename
type cmdProjectRename struct {
	global  *cmdGlobal
	project *cmdProject
}

func (c *cmdProjectRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("rename [<remote>:]<project> <new-name>")
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename projects")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename projects`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProjectRename) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing project name"))
	}

	// Rename the project
	op, err := resource.server.RenameProject(resource.name, api.ProjectPost{Name: args[1]})
	if err != nil {
		return err
	}

	err = op.Wait()
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Project %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Show
type cmdProjectShow struct {
	global  *cmdGlobal
	project *cmdProject
}

func (c *cmdProjectShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("show [<remote>:]<project>")
	cmd.Short = i18n.G("Show project options")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show project options`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProjectShow) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing project name"))
	}

	// Show the project
	project, _, err := resource.server.GetProject(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&project)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Switch project
type cmdProjectSwitch struct {
	global  *cmdGlobal
	project *cmdProject
}

func (c *cmdProjectSwitch) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("switch [<remote>:] <project>")
	cmd.Short = i18n.G("Switch the current project")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Switch the current project`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdProjectSwitch) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	remote := conf.DefaultRemote
	project := args[0]
	if len(args) > 1 {
		remote = args[0]
		project = args[1]
	}

	// make sure the remote exists
	rc, ok := conf.Remotes[remote]
	if !ok {
		return fmt.Errorf(i18n.G("Remote %s doesn't exist"), remote)
	}

	rc.Project = project

	conf.Remotes[remote] = rc

	return conf.SaveConfig(c.global.confPath)
}
