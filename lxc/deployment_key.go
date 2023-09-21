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

type cmdDeploymentKey struct {
	global *cmdGlobal
}

func (c *cmdDeploymentKey) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("key")
	cmd.Short = i18n.G("Manage deployment keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Manage deployment keys"))

	// List.
	deploymentKeyListCmd := cmdDeploymentKeyList{global: c.global, deploymentKey: c}
	cmd.AddCommand(deploymentKeyListCmd.Command())

	// Show.
	deploymentKeyShowCmd := cmdDeploymentKeyShow{global: c.global, deploymentKey: c}
	cmd.AddCommand(deploymentKeyShowCmd.Command())

	// Get.
	deploymentKeyGetCmd := cmdDeploymentKeyGet{global: c.global, deploymentKey: c}
	cmd.AddCommand(deploymentKeyGetCmd.Command())

	// Create.
	deploymentKeyCreateCmd := cmdDeploymentKeyCreate{global: c.global, deploymentKey: c}
	cmd.AddCommand(deploymentKeyCreateCmd.Command())

	// Set.
	deploymentKeySetCmd := cmdDeploymentKeySet{global: c.global, deploymentKey: c}
	cmd.AddCommand(deploymentKeySetCmd.Command())

	// Unset.
	deploymentKeyUnsetCmd := cmdDeploymentKeyUnset{global: c.global, deploymentKey: c, deploymentKeySet: &deploymentKeySetCmd}
	cmd.AddCommand(deploymentKeyUnsetCmd.Command())

	// Edit.
	deploymentKeyEditCmd := cmdDeploymentKeyEdit{global: c.global, deploymentKey: c}
	cmd.AddCommand(deploymentKeyEditCmd.Command())

	// Rename.
	deploymentKeyRenameCmd := cmdDeploymentKeyRename{global: c.global, deploymentKey: c}
	cmd.AddCommand(deploymentKeyRenameCmd.Command())

	// Delete.
	deploymentKeyDeleteCmd := cmdDeploymentKeyDelete{global: c.global, deploymentKey: c}
	cmd.AddCommand(deploymentKeyDeleteCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdDeploymentKeyList struct {
	global        *cmdGlobal
	deploymentKey *cmdDeploymentKey

	flagFormat string
}

func (c *cmdDeploymentKeyList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]<deployment>"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available deployment keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("List available deployment keys"))

	cmd.RunE = c.Run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	return cmd
}

func (c *cmdDeploymentKeyList) Run(cmd *cobra.Command, args []string) error {
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

	// List the deployment keys.
	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing deployment name"))
	}

	deploymentKeys, err := resource.server.GetDeploymentKeys(resource.name)
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, deploymentKey := range deploymentKeys {
		data = append(data, []string{
			deploymentKey.Name,
			deploymentKey.Description,
			deploymentKey.Role,
			deploymentKey.CertificateFingerprint,
		})
	}

	sort.Sort(cli.ByNameAndType(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESC"),
		i18n.G("ROLE"),
		i18n.G("CERTIFICATE FINGERPRINT"),
	}

	return cli.RenderTable(c.flagFormat, header, data, deploymentKeys)
}

// Show.
type cmdDeploymentKeyShow struct {
	global        *cmdGlobal
	deploymentKey *cmdDeploymentKey
}

func (c *cmdDeploymentKeyShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<deployment> <deployment_key>"))
	cmd.Short = i18n.G("Show details of a deployment key")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Show details of a deployment key"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentKeyShow) Run(cmd *cobra.Command, args []string) error {
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

	deploymentKeyName := args[1]
	if deploymentKeyName == "" {
		return fmt.Errorf(i18n.G("Missing deployment key name"))
	}

	// Show the deployment key config.
	deploymentKey, _, err := resource.server.GetDeploymentKey(resource.name, deploymentKeyName)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(deploymentKey)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Get.
type cmdDeploymentKeyGet struct {
	global        *cmdGlobal
	deploymentKey *cmdDeploymentKey

	flagIsProperty bool
}

func (c *cmdDeploymentKeyGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<deployment> <deployment_key> <key>"))
	cmd.Short = i18n.G("Get a deployment key configuration key or property")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Get the value of a deployment key configuration key or property"))

	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a deployment key property"))
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentKeyGet) Run(cmd *cobra.Command, args []string) error {
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

	deploymentKeyName := args[1]
	if deploymentKeyName == "" {
		return fmt.Errorf(i18n.G("Missing deployment key name"))
	}

	deploymentKey, _, err := resource.server.GetDeploymentKey(resource.name, deploymentKeyName)
	if err != nil {
		return err
	}

	res, err := getFieldByJsonTag(*deploymentKey, args[2])
	if err != nil {
		return fmt.Errorf(i18n.G("The property %q does not exist on the deployment key %q: %v"), args[2], args[1], err)
	}

	fmt.Printf("%v\n", res)
	return nil
}

// Create.
type cmdDeploymentKeyCreate struct {
	global        *cmdGlobal
	deploymentKey *cmdDeploymentKey

	role        string
	description string
}

func (c *cmdDeploymentKeyCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<deployment> <new_deployment_key> <certificate_fingerprint>"))
	cmd.Short = i18n.G("Create new deployment keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Create new deployment keys"))

	cmd.Flags().StringVar(&c.role, "role", "", i18n.G("Role given to this deployment key (read-only|ro, read-write|rw|admin)")+"``")
	cmd.Flags().StringVar(&c.description, "description", "", i18n.G("Description of the deployment key")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentKeyCreate) Run(cmd *cobra.Command, args []string) error {
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

	deploymentKeyName := args[1]
	if deploymentKeyName == "" {
		return fmt.Errorf(i18n.G("Missing deployment key name"))
	}

	certificateFingerprint := args[2]
	if certificateFingerprint == "" {
		return fmt.Errorf(i18n.G("Missing certificate fingerprint"))
	}

	// If stdin isn't a terminal, read yaml from it.
	var deploymentKeyPut api.DeploymentKeyPut
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.UnmarshalStrict(contents, &deploymentKeyPut)
		if err != nil {
			return err
		}
	}

	// If the user explicitly set the description and role, use these.
	if c.description != "" {
		deploymentKeyPut.Description = c.description
	}

	if c.role != "" {
		deploymentKeyPut.Role = c.role
	}

	// Create the deployment key.
	deploymentKey := api.DeploymentKeysPost{
		DeploymentKeyPost: api.DeploymentKeyPost{
			Name: deploymentKeyName,
		},
		DeploymentKeyPut:       deploymentKeyPut,
		CertificateFingerprint: certificateFingerprint,
	}

	err = resource.server.CreateDeploymentKey(resource.name, deploymentKey)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Deployment key %q created.")+"\n", deploymentKeyName)
	}

	return nil
}

// Set.
type cmdDeploymentKeySet struct {
	global        *cmdGlobal
	deploymentKey *cmdDeploymentKey
}

func (c *cmdDeploymentKeySet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<deployment> <deployment_key> <key>=<value>..."))
	cmd.Short = i18n.G("Set deployment key configuration keys or properties")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Set deployment key configuration keys or properties"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentKeySet) Run(cmd *cobra.Command, args []string) error {
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

	deploymentKeyName := args[1]
	if deploymentKeyName == "" {
		return fmt.Errorf(i18n.G("Missing deployment key name"))
	}

	// Get the deployment key.
	deploymentKey, etag, err := resource.server.GetDeploymentKey(resource.name, deploymentKeyName)
	if err != nil {
		return err
	}

	// Get the new config keys
	keys, err := getConfig(args[2:]...)
	if err != nil {
		return err
	}

	writable := deploymentKey.Writable()
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

	return resource.server.UpdateDeploymentKey(resource.name, deploymentKeyName, writable, etag)
}

// Unset.
type cmdDeploymentKeyUnset struct {
	global           *cmdGlobal
	deploymentKey    *cmdDeploymentKey
	deploymentKeySet *cmdDeploymentKeySet
}

func (c *cmdDeploymentKeyUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<deployment> <deployment_key> <key>"))
	cmd.Short = i18n.G("Unset deployment key configuration keys or properties")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Unset deployment key configuration keys or properties"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentKeyUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 3, 3)
	if exit {
		return err
	}

	args = append(args, "")
	return c.deploymentKeySet.Run(cmd, args)
}

// Edit.
type cmdDeploymentKeyEdit struct {
	global        *cmdGlobal
	deploymentKey *cmdDeploymentKey
}

func (c *cmdDeploymentKeyEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<deployment> <deployment_key>"))
	cmd.Short = i18n.G("Edit deployment key configuration")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Edit deployment key configuration"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentKeyEdit) helpTemplate() string {
	return i18n.G(
		`### This is a yaml representation of the deployment key.
### Any line starting with a '# will be ignored.
###
### A deployment key description and configuration items.
###
### An example would look like:
### name: test-deployment-key
### description: test deployment key description
### role: admin
###
###`)
}

func (c *cmdDeploymentKeyEdit) Run(cmd *cobra.Command, args []string) error {
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

	deploymentKeyName := args[1]
	if deploymentKeyName == "" {
		return fmt.Errorf(i18n.G("Missing deployment key name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		// Allow output of `lxc deployment key show` command to be passed in here, but only take the contents
		// of the DeploymentKeyPut fields when updating the deployment key. The other fields are silently discarded.
		newdata := api.DeploymentKeyPut{}
		err = yaml.UnmarshalStrict(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateDeploymentKey(resource.name, deploymentKeyName, newdata, "")
	}

	// Get the current config.
	deploymentKey, etag, err := resource.server.GetDeploymentKey(resource.name, deploymentKeyName)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(deploymentKey.Writable())
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
		newdata := api.DeploymentKeyPut{} // We show the full deployment key info, but only send the writable fields.
		err = yaml.UnmarshalStrict(content, &newdata)
		if err == nil {
			err = resource.server.UpdateDeploymentKey(resource.name, deploymentKeyName, newdata, etag)
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
type cmdDeploymentKeyRename struct {
	global        *cmdGlobal
	deploymentKey *cmdDeploymentKey
}

func (c *cmdDeploymentKeyRename) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<deployment> <deployment_key> <new_deployment_key>"))
	cmd.Short = i18n.G("Rename deployment keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Rename deployment keys"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentKeyRename) Run(cmd *cobra.Command, args []string) error {
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

	oldDeploymentKeyName := args[1]
	if oldDeploymentKeyName == "" {
		return fmt.Errorf(i18n.G("Missing deployment key name"))
	}

	newDeploymentKeyName := args[2]
	if newDeploymentKeyName == "" {
		return fmt.Errorf(i18n.G("Missing new deployment key name"))
	}

	// Rename the deployment key.
	err = resource.server.RenameDeploymentKey(resource.name, oldDeploymentKeyName, api.DeploymentKeyPost{Name: newDeploymentKeyName})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Deployment key %q renamed to %q")+"\n", oldDeploymentKeyName, args[2])
	}

	return nil
}

// Delete.
type cmdDeploymentKeyDelete struct {
	global        *cmdGlobal
	deploymentKey *cmdDeploymentKey
}

func (c *cmdDeploymentKeyDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<deployment> <deployment_key>"))
	cmd.Short = i18n.G("Delete deployment keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G("Delete deployment keys"))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDeploymentKeyDelete) Run(cmd *cobra.Command, args []string) error {
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

	deploymentKeyName := args[1]
	if deploymentKeyName == "" {
		return fmt.Errorf(i18n.G("Missing deployment key name"))
	}

	// Delete the deployment key.
	err = resource.server.DeleteDeploymentKey(resource.name, deploymentKeyName)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Deployment key %q deleted")+"\n", deploymentKeyName)
	}

	return nil
}
