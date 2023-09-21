package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v2"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/termios"
)

type cmdDeployment struct {
	global *cmdGlobal
}

func (c *cmdDeployment) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("deployment")
	cmd.Short = i18n.G("Manage deployments")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage deployments"))

	// List.
	deploymentListCmd := cmdDeploymentList{global: c.global, deployment: c}
	cmd.AddCommand(deploymentListCmd.Command())

	// Show.
	deploymentShowCmd := cmdDeploymentShow{global: c.global, deployment: c}
	cmd.AddCommand(deploymentShowCmd.Command())

	// Get.
	deploymentGetCmd := cmdDeploymentGet{global: c.global, deployment: c}
	cmd.AddCommand(deploymentGetCmd.Command())

	// Create.
	deploymentCreateCmd := cmdDeploymentCreate{global: c.global, deployment: c}
	cmd.AddCommand(deploymentCreateCmd.Command())

	// Set.
	deploymentSetCmd := cmdDeploymentSet{global: c.global, deployment: c}
	cmd.AddCommand(deploymentSetCmd.Command())

	// Unset.
	deploymentUnsetCmd := cmdDeploymentUnset{global: c.global, deployment: c, deploymentSet: &deploymentSetCmd}
	cmd.AddCommand(deploymentUnsetCmd.Command())

	// Edit.
	deploymentEditCmd := cmdDeploymentEdit{global: c.global, deployment: c}
	cmd.AddCommand(deploymentEditCmd.Command())

	// Rename.
	deploymentRenameCmd := cmdDeploymentRename{global: c.global, deployment: c}
	cmd.AddCommand(deploymentRenameCmd.Command())

	// Delete.
	deploymentDeleteCmd := cmdDeploymentDelete{global: c.global, deployment: c}
	cmd.AddCommand(deploymentDeleteCmd.Command())

	// Key.
	deploymentKeyCmd := cmdDeploymentKey{global: c.global}
	cmd.AddCommand(deploymentKeyCmd.Command())

	// Shape.
	deploymentShapeCmd := cmdDeploymentShape{global: c.global}
	cmd.AddCommand(deploymentShapeCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdDeploymentList struct {
	global     *cmdGlobal
	deployment *cmdDeployment

	flagFormat string
}

func (c *cmdDeploymentList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available deployments")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List available deployments"))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	return cmd
}

func (c *cmdDeploymentList) Run(cmd *cobra.Command, args []string) error {
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

	// List the deployments.
	if resource.name != "" {
		return fmt.Errorf(i18n.G("Filtering isn't supported yet"))
	}

	deployments, err := resource.server.GetDeployments()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, deployment := range deployments {
		strUsedBy := fmt.Sprintf("%d", len(deployment.UsedBy))
		details := []string{
			deployment.Name,
			deployment.Description,
			deployment.GovernorWebhookURL,
			strUsedBy,
		}

		data = append(data, details)
	}

	sort.Sort(cli.ByNameAndType(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("GOVERNOR WEBHOOK URL"),
		i18n.G("USED BY"),
	}

	return cli.RenderTable(c.flagFormat, header, data, deployments)
}

// Show.
type cmdDeploymentShow struct {
	global     *cmdGlobal
	deployment *cmdDeployment
}

func (c *cmdDeploymentShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<deployment>"))
	cmd.Short = i18n.G("Show deployment configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Show deployment configurations"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentShow) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing deployment name"))
	}

	// Show the deployment config.
	deployment, _, err := resource.server.GetDeployment(resource.name)
	if err != nil {
		return err
	}

	sort.Strings(deployment.UsedBy)

	data, err := yaml.Marshal(&deployment)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Get.
type cmdDeploymentGet struct {
	global     *cmdGlobal
	deployment *cmdDeployment

	flagIsProperty bool
}

func (c *cmdDeploymentGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<deployment> <key>"))
	cmd.Short = i18n.G("Get values for deployment configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Get values for deployment configuration keys"))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a deployment property"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentGet) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing deployment name"))
	}

	deployment, _, err := resource.server.GetDeployment(resource.name)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := deployment.Writable()
		res, err := getFieldByJsonTag(&w, args[1])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the deployment %q: %v"), args[1], resource.name, err)
		}

		fmt.Printf("%v\n", res)
		return nil
	}

	value, ok := deployment.Config[args[1]]
	if !ok {
		return fmt.Errorf(i18n.G("The key %q does not exist on the deployment %q"), args[1], resource.name)
	}

	fmt.Printf("%s\n", value)
	return nil
}

// Create.
type cmdDeploymentCreate struct {
	global     *cmdGlobal
	deployment *cmdDeployment

	governorWebhookURL string
	description        string
}

func (c *cmdDeploymentCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<deployment> [key=value...]"))
	cmd.Short = i18n.G("Create new deployments")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create new deployments"))

	cmd.Flags().StringVar(&c.governorWebhookURL, "governor-webhook-url", "", i18n.G("Governor webhook URL for provider triggered scaling requests")+"``")
	cmd.Flags().StringVar(&c.description, "description", "", i18n.G("Description of the deployment")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentCreate) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing deployment name"))
	}

	// If stdin isn't a terminal, read yaml from it.
	var deploymentPut api.DeploymentPut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &deploymentPut)
		if err != nil {
			return err
		}
	}

	// If the user explicitly set the description and webhook URL, use these.
	if c.description != "" {
		deploymentPut.Description = c.description
	}

	if c.governorWebhookURL != "" {
		deploymentPut.GovernorWebhookURL = c.governorWebhookURL
	}

	// Create the deployment.
	deployment := api.DeploymentsPost{
		DeploymentPost: api.DeploymentPost{
			Name: resource.name,
		},
		DeploymentPut: deploymentPut,
	}

	if deployment.Config == nil {
		deployment.Config = map[string]string{}
	}

	for i := 1; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf(i18n.G("Bad key/value pair: %s"), args[i])
		}

		deployment.Config[entry[0]] = entry[1]
	}

	err = resource.server.CreateDeployment(deployment)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Deployment %q created")+"\n", resource.name)
	}

	return nil
}

// Set.
type cmdDeploymentSet struct {
	global     *cmdGlobal
	deployment *cmdDeployment

	flagIsProperty bool
}

func (c *cmdDeploymentSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<deployment> <key>=<value>..."))
	cmd.Short = i18n.G("Set deployment configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Set deployment configuration keys"))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a deployment property"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentSet) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing deployment name"))
	}

	// Get the deployment.
	deployment, etag, err := resource.server.GetDeployment(resource.name)
	if err != nil {
		return err
	}

	// Get the new config keys
	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	writable := deployment.Writable()
	if c.flagIsProperty {
		if cmd.Name() == "unset" {
			for k := range keys {
				err := unsetFieldByJsonTag(&writable, k)
				if err != nil {
					return fmt.Errorf(i18n.G("Error unsetting property: %v"), err)
				}
			}
		} else {
			err := unpackKVToWritable(&writable, keys)
			if err != nil {
				return fmt.Errorf(i18n.G("Error setting properties: %v"), err)
			}
		}
	} else {
		for k, v := range keys {
			writable.Config[k] = v
		}
	}

	return resource.server.UpdateDeployment(resource.name, writable, etag)
}

// Unset.
type cmdDeploymentUnset struct {
	global        *cmdGlobal
	deployment    *cmdDeployment
	deploymentSet *cmdDeploymentSet

	flagIsProperty bool
}

func (c *cmdDeploymentUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<deployment> <key>"))
	cmd.Short = i18n.G("Unset deployment configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Unset deployment configuration keys"))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a deployment property"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.deploymentSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.deploymentSet.Run(cmd, args)
}

// Edit.
type cmdDeploymentEdit struct {
	global     *cmdGlobal
	deployment *cmdDeployment
}

func (c *cmdDeploymentEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<deployment>"))
	cmd.Short = i18n.G("Edit deployment configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Edit deployment configurations as YAML"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the deployment.
### Any line starting with a '# will be ignored.
###
### A deployment description and configuration items.
###
### An example would look like:
### name: test-deployment
### description: test deployment description
### config:
###  user.foo: bah
###
### Note that only the description and configuration keys can be changed.`)
}

func (c *cmdDeploymentEdit) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing deployment name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `lxc deployment show` command to be passed in here, but only take the contents
		// of the DeploymentPut fields when updating the deployment. The other fields are silently discarded.
		newdata := api.DeploymentPut{}
		err = yaml.UnmarshalStrict(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateDeployment(resource.name, newdata, "")
	}

	// Get the current config.
	deployment, etag, err := resource.server.GetDeployment(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(deployment.Writable())
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
		newdata := api.DeploymentPut{} // We show the full deployment info, but only send the writable fields.
		err = yaml.UnmarshalStrict(content, &newdata)
		if err == nil {
			err = resource.server.UpdateDeployment(resource.name, newdata, etag)
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

// Rename.
type cmdDeploymentRename struct {
	global     *cmdGlobal
	deployment *cmdDeployment
}

func (c *cmdDeploymentRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<deployment> <new-name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename deployments")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Rename deployments"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentRename) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing deployment name"))
	}

	// Rename the network.
	err = resource.server.RenameDeployment(resource.name, api.DeploymentPost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Deployment %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Delete.
type cmdDeploymentDelete struct {
	global     *cmdGlobal
	deployment *cmdDeployment
}

func (c *cmdDeploymentDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<deployment>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete network deployments")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete network deployments"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentDelete) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing deployment name"))
	}

	// Delete the deployment.
	err = resource.server.DeleteDeployment(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Deployment %s deleted")+"\n", resource.name)
	}

	return nil
}
