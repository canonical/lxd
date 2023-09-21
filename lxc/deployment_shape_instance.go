package main

import (
	"fmt"
	"net/url"
	"path"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdDeploymentShapeInstance struct {
	global *cmdGlobal
}

func (c *cmdDeploymentShapeInstance) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("instance")
	cmd.Short = i18n.G("Manage instances within a deployment shape")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage instances within a deployment shape"))

	// Launch an instance within a deployment shape.
	deploymentShapeInstanceLaunchCmd := cmdDeploymentShapeInstanceLaunch{global: c.global, deploymentShapeInstance: c}
	cmd.AddCommand(deploymentShapeInstanceLaunchCmd.Command())

	// Delete an instance within a deployment shape.
	deploymentShapeInstanceDeleteCmd := cmdDeploymentShapeInstanceDelete{global: c.global, deploymentShapeInstance: c}
	cmd.AddCommand(deploymentShapeInstanceDeleteCmd.Command())

	// List instances within a deployment shape.
	deploymentShapeInstanceListCmd := cmdDeploymentShapeInstanceList{global: c.global, deploymentShapeInstance: c}
	cmd.AddCommand(deploymentShapeInstanceListCmd.Command())

	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Add instance within a deployment shape.
type cmdDeploymentShapeInstanceLaunch struct {
	global                  *cmdGlobal
	deploymentShapeInstance *cmdDeploymentShapeInstance
}

func (c *cmdDeploymentShapeInstanceLaunch) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("launch", i18n.G("[<remote>:]<deployment> <deployment_shape> <new_instance_name>"))
	cmd.Short = i18n.G("Launch a new instance in a deployment shape")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Launch a new instance in deployment shape"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentShapeInstanceLaunch) Run(cmd *cobra.Command, args []string) error {
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

	instanceName := args[2]
	if instanceName == "" {
		return fmt.Errorf(i18n.G("Missing instance name"))
	}

	req := api.DeploymentInstancesPost{
		ShapeName:    deploymentShapeName,
		InstanceName: instanceName,
	}

	op, err := resource.server.AddInstanceToDeploymentShape(resource.name, req)
	if err != nil {
		return err
	}

	// Watch the background operation
	progress := cli.ProgressRenderer{
		Format: i18n.G("Retrieving image: %s"),
		Quiet:  c.global.flagQuiet,
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

	// Extract the instance name
	info, err := op.GetTarget()
	if err != nil {
		return err
	}

	instances, ok := info.Resources["instances"]
	if !ok || len(instances) == 0 {
		// Try using the older "containers" field
		instances, ok = info.Resources["containers"]
		if !ok || len(instances) == 0 {
			return fmt.Errorf(i18n.G("Didn't get any affected image, instance or snapshot from server"))
		}
	}

	if len(instances) == 1 {
		url, err := url.Parse(instances[0])
		if err != nil {
			return err
		}

		instanceName = path.Base(url.Path)
		if !c.global.flagQuiet {
			fmt.Printf(i18n.G("Instance %q added in deployment shape %q")+"\n", instanceName, deploymentShapeName)
		}
	}

	startReq := api.InstanceStatePut{
		Action:  "start",
		Timeout: -1,
	}

	opStart, err := resource.server.UpdateDeploymentInstanceState(resource.name, deploymentShapeName, instanceName, startReq, "")
	if err != nil {
		return err
	}

	progress = cli.ProgressRenderer{
		Quiet: c.global.flagQuiet,
	}

	_, err = opStart.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for operation to finish
	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return fmt.Errorf("%s\n"+i18n.G("Try `lxc info --show-log %s` for more info"), err, instanceName)
	}

	progress.Done("")

	return nil
}

// Delete instance within a deployment shape.
type cmdDeploymentShapeInstanceDelete struct {
	global                  *cmdGlobal
	deploymentShapeInstance *cmdDeploymentShapeInstance
}

func (c *cmdDeploymentShapeInstanceDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<deployment> <deployment_shape> <instance_name>"))
	cmd.Short = i18n.G("Delete an instance from a deployment shape")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete an instance from a deployment shape"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentShapeInstanceDelete) Run(cmd *cobra.Command, args []string) error {
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

	instanceName := args[2]
	if instanceName == "" {
		return fmt.Errorf(i18n.G("Missing instance name"))
	}

	op, err := resource.server.DeleteInstanceInDeploymentShape(resource.name, deploymentShapeName, instanceName)
	if err != nil {
		return err
	}

	return op.Wait()
}

// List instances within instance set.
type cmdDeploymentShapeInstanceList struct {
	global                  *cmdGlobal
	deploymentShapeInstance *cmdDeploymentShapeInstance

	flagFormat string
}

func (c *cmdDeploymentShapeInstanceList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]<deployment> <deployment_shape>"))
	cmd.Short = i18n.G("List instances within a deployment shape")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List instances within a deployment shape"))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	return cmd
}

func (c *cmdDeploymentShapeInstanceList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

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

	instances, err := resource.server.GetInstancesInDeploymentShape(resource.name, deploymentShapeName)
	if err != nil {
		return err
	}

	conf := c.global.conf

	// Connect to LXD
	d, err := conf.GetInstanceServer(conf.DefaultRemote)
	if err != nil {
		return err
	}

	// Get the list of columns
	cmdList := &cmdList{global: c.global, flagColumns: defaultColumns, flagFormat: c.flagFormat}
	columns, _, err := cmdList.parseColumns(d.IsClustered())
	if err != nil {
		return err
	}

	// Apply filters
	instancesFiltered := []api.Instance{}
	_, clientFilters := getServerSupportedFilters([]string{}, api.Instance{})
	for _, inst := range instances {
		if !cmdList.shouldShow(clientFilters, &inst, nil, true) {
			continue
		}

		instancesFiltered = append(instancesFiltered, inst)
	}

	// List the instances
	return cmdList.listInstances(conf, d, instancesFiltered, clientFilters, columns)
}
