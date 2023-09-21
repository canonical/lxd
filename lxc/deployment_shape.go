package main

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spf13/cobra"
	yaml "gopkg.in/yaml.v2"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/termios"
)

type cmdDeploymentShape struct {
	global *cmdGlobal
}

func (c *cmdDeploymentShape) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("shape")
	cmd.Short = i18n.G("Manage deployment shapes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage deployment shapes"))

	// List.
	deploymentShapeListCmd := cmdDeploymentShapeList{global: c.global, deploymentShape: c}
	cmd.AddCommand(deploymentShapeListCmd.Command())

	// Show.
	deploymentShapeShowCmd := cmdDeploymentShapeShow{global: c.global, deploymentShape: c}
	cmd.AddCommand(deploymentShapeShowCmd.Command())

	// Get.
	deploymentShapeGetCmd := cmdDeploymentShapeGet{global: c.global, deploymentShape: c}
	cmd.AddCommand(deploymentShapeGetCmd.Command())

	// Create.
	deploymentShapeCreateCmd := cmdDeploymentShapeCreate{global: c.global, deploymentShape: c, image: &cmdImage{global: c.global}}
	cmd.AddCommand(deploymentShapeCreateCmd.Command())

	// Set.
	deploymentShapeSetCmd := cmdDeploymentShapeSet{global: c.global, deploymentShape: c}
	cmd.AddCommand(deploymentShapeSetCmd.Command())

	// Unset.
	deploymentShapeUnsetCmd := cmdDeploymentShapeUnset{global: c.global, deploymentShape: c, deploymentShapeSet: &deploymentShapeSetCmd}
	cmd.AddCommand(deploymentShapeUnsetCmd.Command())

	// Edit.
	deploymentShapeEditCmd := cmdDeploymentShapeEdit{global: c.global, deploymentShape: c}
	cmd.AddCommand(deploymentShapeEditCmd.Command())

	// Rename.
	deploymentShapeRenameCmd := cmdDeploymentShapeRename{global: c.global, deploymentShape: c}
	cmd.AddCommand(deploymentShapeRenameCmd.Command())

	// Delete.
	deploymentShapeDeleteCmd := cmdDeploymentShapeDelete{global: c.global, deploymentShape: c}
	cmd.AddCommand(deploymentShapeDeleteCmd.Command())

	// Instance.
	deploymentShapeInstanceCmd := cmdDeploymentShapeInstance{global: c.global}
	cmd.AddCommand(deploymentShapeInstanceCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdDeploymentShapeList struct {
	global          *cmdGlobal
	deploymentShape *cmdDeploymentShape

	flagFormat string
}

func (c *cmdDeploymentShapeList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]<deployment>"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available deployment shapes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List available deployment shapes"))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	return cmd
}

func (c *cmdDeploymentShapeList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
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
	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing deployment name"))
	}

	deploymentShapes, err := resource.server.GetDeploymentShapes(resource.name)
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, deploymentShape := range deploymentShapes {
		data = append(data, []string{
			deploymentShape.Name,
			deploymentShape.Description,
			fmt.Sprint(deploymentShape.ScalingMinimum),
			fmt.Sprint(deploymentShape.ScalingCurrent),
			fmt.Sprint(deploymentShape.ScalingMaximum),
		})
	}

	sort.Sort(cli.ByNameAndType(data))

	header := []string{
		i18n.G("SHAPE NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("SCALING MIN"),
		i18n.G("SCALING CURRENT"),
		i18n.G("SCALING MAX"),
	}

	return cli.RenderTable(c.flagFormat, header, data, deploymentShapes)
}

// Show.
type cmdDeploymentShapeShow struct {
	global          *cmdGlobal
	deploymentShape *cmdDeploymentShape
}

func (c *cmdDeploymentShapeShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<deployment> <deployment_shape>"))
	cmd.Short = i18n.G("Show details of a deployment shape")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Show details of a deployment shape"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentShapeShow) Run(cmd *cobra.Command, args []string) error {
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

	deploymentShapeName := args[1]
	if deploymentShapeName == "" {
		return fmt.Errorf(i18n.G("Missing deployment shape name"))
	}

	// Show the deployment config.
	deploymentShape, _, err := resource.server.GetDeploymentShape(resource.name, deploymentShapeName)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(deploymentShape)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Get.
type cmdDeploymentShapeGet struct {
	global          *cmdGlobal
	deploymentShape *cmdDeploymentShape

	flagIsProperty bool
}

func (c *cmdDeploymentShapeGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<deployment> <deployment_shape> <key>"))
	cmd.Short = i18n.G("Get a deployment shape configuration key or property")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Get the value for a deployment shape configuration key or property"))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a deployment shape property"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentShapeGet) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
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

	deploymentShapeName := args[1]
	if deploymentShapeName == "" {
		return fmt.Errorf(i18n.G("Missing deployment shape name"))
	}

	deploymentShape, _, err := resource.server.GetDeploymentShape(resource.name, deploymentShapeName)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := deploymentShape.Writable()
		res, err := getFieldByJsonTag(&w, args[2])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the deployment shape %q: %v"), args[2], args[1], err)
		}

		fmt.Printf("%v\n", res)
		return nil
	}

	value, ok := deploymentShape.Config[args[2]]
	if !ok {
		return fmt.Errorf(i18n.G("The key %q does not exist on the deployment shape %q"), args[2], args[1])
	}

	fmt.Printf("%s\n", value)
	return nil
}

// Create.
type cmdDeploymentShapeCreate struct {
	global          *cmdGlobal
	deploymentShape *cmdDeploymentShape
	image           *cmdImage

	description  string
	scaling_max  int
	scaling_min  int
	from_profile string
	from_image   string
	vm           bool
}

func (c *cmdDeploymentShapeCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<deployment> <new_deployment_shape>"))
	cmd.Short = i18n.G("Create new deployment shapes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create new deployment shapes"))

	cmd.Flags().StringVar(&c.description, "description", "", i18n.G("Description of the deployment")+"``")
	cmd.Flags().IntVar(&c.scaling_max, "scaling-max", 0, i18n.G("Maximum number of instances the deployment scale can handle")+"``")
	cmd.Flags().IntVar(&c.scaling_min, "scaling-min", 0, i18n.G("Minimum number of instances the deployment scale can handle")+"``")
	cmd.Flags().StringVarP(&c.from_profile, "from-profile", "p", "", i18n.G("Prefill the instance template of this new deployment shape from a profile.")+"``")
	cmd.Flags().StringVarP(&c.from_image, "from-image", "i", "", i18n.G("Prefill the instance template source of this new deployment shape from an image.")+"``")
	cmd.Flags().BoolVar(&c.vm, "vm", false, i18n.G("Whether the shape handles virtual machines (default: containers)")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentShapeCreate) Run(cmd *cobra.Command, args []string) error {
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

	deploymentShapeName := args[1]
	if deploymentShapeName == "" {
		return fmt.Errorf(i18n.G("Missing deployment shape name"))
	}

	// If stdin isn't a terminal, read yaml from it.
	deploymentShapePut := api.DeploymentShapePut{}

	if c.description != "" {
		deploymentShapePut.Description = c.description
	}

	// The validation rules for the scaling min/max are handled by the server.
	if c.scaling_min != 0 {
		deploymentShapePut.ScalingMinimum = c.scaling_min
	}

	if c.scaling_max != 0 {
		deploymentShapePut.ScalingMaximum = c.scaling_max
	}

	deploymentShapePut.InstanceTemplate = api.InstancesPost{}
	if c.vm {
		deploymentShapePut.InstanceTemplate.Type = "virtual-machine"
	} else {
		deploymentShapePut.InstanceTemplate.Type = "container"
	}

	if c.from_profile != "" {
		// Try to fetch the profile
		profile, _, err := resource.server.GetProfile(c.from_profile)
		if err != nil {
			return err
		}

		// Insert the config and the devices into the deployment shape template.

		deploymentShapePut.InstanceTemplate.InstancePut = api.InstancePut{}
		deploymentShapePut.InstanceTemplate.InstancePut.Config = profile.Config
		deploymentShapePut.InstanceTemplate.InstancePut.Devices = profile.Devices
	}

	if c.from_image != "" {
		imageRemote, imageAlias, err := c.global.conf.ParseRemote(c.from_image)
		if err != nil {
			return err
		}

		d, err := c.global.conf.GetInstanceServer("local")
		if err != nil {
			return err
		}

		imageRemote, imageAlias = guessImage(c.global.conf, d, "local", imageRemote, imageAlias)
		if imageAlias == "" {
			imageAlias = "default"
		}

		source := api.InstanceSource{}
		source.Type = "image"
		imgServer, imgInfo, err := getImgInfo(d, c.global.conf, imageRemote, "local", imageAlias, &source)
		if err != nil {
			return err
		}

		connectionInfo, err := d.GetSourceImageConnectionInfo(imgServer, *imgInfo, &source)
		if err != nil {
			return err
		}

		if connectionInfo != nil {
			if len(connectionInfo.Addresses) == 0 {
				return fmt.Errorf(i18n.G("The image doesn't have any associated address"))
			}

			source.Certificate = connectionInfo.Certificate
			source.Protocol = connectionInfo.Protocol
			source.Server = connectionInfo.Addresses[0]
		}

		// Insert the source into the deployment shape template.
		if shared.IsZero(deploymentShapePut.InstanceTemplate) {
			deploymentShapePut.InstanceTemplate = api.InstancesPost{}
		}

		deploymentShapePut.InstanceTemplate.Source = source
	}

	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &deploymentShapePut)
		if err != nil {
			return err
		}
	}

	// Create the deployment shape.
	deploymentShape := api.DeploymentShapesPost{
		DeploymentShapePost: api.DeploymentShapePost{
			Name: deploymentShapeName,
		},
		DeploymentShapePut: deploymentShapePut,
	}

	err = resource.server.CreateDeploymentShape(resource.name, deploymentShape)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Deployment shape %q created")+"\n", deploymentShapeName)
	}

	return nil
}

// Set.
type cmdDeploymentShapeSet struct {
	global          *cmdGlobal
	deploymentShape *cmdDeploymentShape

	flagIsProperty bool
}

func (c *cmdDeploymentShapeSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<deployment> <deployment_shape> <key>=<value>..."))
	cmd.Short = i18n.G("Set deployment shape configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Set deployment shape configuration keys"))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a deployment shape property"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentShapeSet) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, -1)
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

	deploymentShapeName := args[1]
	if deploymentShapeName == "" {
		return fmt.Errorf(i18n.G("Missing deployment shape name"))
	}

	// Get the deployment shape.
	deploymentShape, etag, err := resource.server.GetDeploymentShape(resource.name, deploymentShapeName)
	if err != nil {
		return err
	}

	// Get the new config keys
	keys, err := getConfig(args[2:]...)
	if err != nil {
		return err
	}

	writable := deploymentShape.Writable()
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

	return resource.server.UpdateDeploymentShape(resource.name, deploymentShapeName, writable, etag)
}

// Unset.
type cmdDeploymentShapeUnset struct {
	global             *cmdGlobal
	deploymentShape    *cmdDeploymentShape
	deploymentShapeSet *cmdDeploymentShapeSet

	flagIsProperty bool
}

func (c *cmdDeploymentShapeUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<deployment> <deployment_shape> <key>"))
	cmd.Short = i18n.G("Unset deployment shape configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Unset deployment shape configuration keys"))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a deployment shape property"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentShapeUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	c.deploymentShapeSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.deploymentShapeSet.Run(cmd, args)
}

// Edit.
type cmdDeploymentShapeEdit struct {
	global          *cmdGlobal
	deploymentShape *cmdDeploymentShape
}

func (c *cmdDeploymentShapeEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<deployment> <deployment_shape>"))
	cmd.Short = i18n.G("Edit deployment shape configuration")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Edit deployment shape configuration"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentShapeEdit) helpTemplate() string {
	return i18n.G(
		`### This is a yaml representation of the deployment shape.
### Any line starting with a '# will be ignored.
###
### A deployment shape description and configuration items.
###
### An example would look like:
### name: test-deployment-shape
### description: test deployment shape description
###
### Note that only the description and configuration keys can be changed.`)
}

func (c *cmdDeploymentShapeEdit) Run(cmd *cobra.Command, args []string) error {
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

	deploymentShapeName := args[1]
	if deploymentShapeName == "" {
		return fmt.Errorf(i18n.G("Missing deployment shape name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `lxc deployment shape show` command to be passed in here, but only take the contents
		// of the ShapePut fields when updating the deployment shape. The other fields are silently discarded.
		newdata := api.DeploymentShapePut{}
		err = yaml.UnmarshalStrict(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateDeploymentShape(resource.name, deploymentShapeName, newdata, "")
	}

	// Get the current config.
	deploymentShape, etag, err := resource.server.GetDeploymentShape(resource.name, deploymentShapeName)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(deploymentShape.Writable())
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
		newdata := api.DeploymentShapePut{} // We show the full deployment shape info, but only send the writable fields.
		err = yaml.UnmarshalStrict(content, &newdata)
		if err == nil {
			err = resource.server.UpdateDeploymentShape(resource.name, deploymentShapeName, newdata, etag)
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
type cmdDeploymentShapeRename struct {
	global          *cmdGlobal
	deploymentShape *cmdDeploymentShape
}

func (c *cmdDeploymentShapeRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<deployment> <deployment_shape> <new_deployment_shape>"))
	cmd.Short = i18n.G("Rename deployment shapes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Rename deployment shapes"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentShapeRename) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
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

	oldDeploymentShapeName := args[1]
	if oldDeploymentShapeName == "" {
		return fmt.Errorf(i18n.G("Missing deployment shape name"))
	}

	newDeploymentShapeName := args[2]
	if newDeploymentShapeName == "" {
		return fmt.Errorf(i18n.G("Missing new deployment shape name"))
	}

	// Rename the instance set.
	err = resource.server.RenameDeploymentShape(resource.name, oldDeploymentShapeName, api.DeploymentShapePost{Name: newDeploymentShapeName})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Deployment shape %q renamed to %q")+"\n", oldDeploymentShapeName, args[2])
	}

	return nil
}

// Delete.
type cmdDeploymentShapeDelete struct {
	global          *cmdGlobal
	deploymentShape *cmdDeploymentShape
}

func (c *cmdDeploymentShapeDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<deployment> <deployment_shape>"))
	cmd.Short = i18n.G("Delete deployment shapes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete deployment shapes"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentShapeDelete) Run(cmd *cobra.Command, args []string) error {
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

	deploymentShapeName := args[1]
	if deploymentShapeName == "" {
		return fmt.Errorf(i18n.G("Missing deployment shape name"))
	}

	// Delete the deployment.
	err = resource.server.DeleteDeploymentShape(resource.name, deploymentShapeName)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Deployment shape %q deleted")+"\n", deploymentShapeName)
	}

	return nil
}
