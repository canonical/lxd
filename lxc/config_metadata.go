package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"syscall"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type cmdConfigMetadata struct {
	global  *cmdGlobal
	config  *cmdConfig
	profile *cmdProfile
}

func (c *cmdConfigMetadata) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("metadata")
	cmd.Short = i18n.G("Manage container metadata files")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage container metadata files`))

	// Edit
	configMetadataEditCmd := cmdConfigMetadataEdit{global: c.global, config: c.config, configMetadata: c}
	cmd.AddCommand(configMetadataEditCmd.Command())

	// Show
	configMetadataShowCmd := cmdConfigMetadataShow{global: c.global, config: c.config, configMetadata: c}
	cmd.AddCommand(configMetadataShowCmd.Command())

	return cmd
}

// Edit
type cmdConfigMetadataEdit struct {
	global         *cmdGlobal
	config         *cmdConfig
	configMetadata *cmdConfigMetadata
}

func (c *cmdConfigMetadataEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("edit [<remote>:]<container>")
	cmd.Short = i18n.G("Edit container metadata files")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit container metadata files`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigMetadataEdit) helpTemplate() string {
	return i18n.G(
		`### This is a yaml representation of the container metadata.
### Any line starting with a '# will be ignored.
###
### A sample configuration looks like:
###
### architecture: x86_64
### creation_date: 1477146654
### expiry_date: 0
### properties:
###   architecture: x86_64
###   description: Busybox x86_64
###   name: busybox-x86_64
###   os: Busybox
### templates:
###   /template:
###     when:
###     - ""
###     create_only: false
###     template: template.tpl
###     properties: {}`)
}

func (c *cmdConfigMetadataEdit) Run(cmd *cobra.Command, args []string) error {
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

	// Edit the metadata
	if !termios.IsTerminal(int(syscall.Stdin)) {
		metadata := api.ImageMetadata{}
		content, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Unmarshal(content, &metadata)
		if err != nil {
			return err
		}
		return resource.server.SetContainerMetadata(resource.name, metadata, "")
	}

	metadata, etag, err := resource.server.GetContainerMetadata(resource.name)
	if err != nil {
		return err
	}
	origContent, err := yaml.Marshal(metadata)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(origContent)))
	if err != nil {
		return err
	}

	for {
		metadata := api.ImageMetadata{}
		err = yaml.Unmarshal(content, &metadata)
		if err == nil {
			err = resource.server.SetContainerMetadata(resource.name, metadata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
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

// Show
type cmdConfigMetadataShow struct {
	global         *cmdGlobal
	config         *cmdConfig
	configMetadata *cmdConfigMetadata
}

func (c *cmdConfigMetadataShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("show [<remote>:]<container>")
	cmd.Short = i18n.G("Show container metadata files")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show container metadata files`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigMetadataShow) Run(cmd *cobra.Command, args []string) error {
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

	// Show the container metadata
	metadata, _, err := resource.server.GetContainerMetadata(resource.name)
	if err != nil {
		return err
	}

	content, err := yaml.Marshal(metadata)
	if err != nil {
		return err
	}
	fmt.Printf("%s", content)

	return nil
}
