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

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/entity"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/termios"
)

type cmdAuth struct {
	global *cmdGlobal
}

func (c *cmdAuth) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("auth")
	cmd.Short = i18n.G("Manage user authorization")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage user authorization`))

	groupCmd := cmdGroup{global: c.global}
	cmd.AddCommand(groupCmd.command())

	permissionCmd := cmdPermission{global: c.global}
	cmd.AddCommand(permissionCmd.command())

	identityCmd := cmdIdentity{global: c.global}
	cmd.AddCommand(identityCmd.command())

	identityProviderGroupCmd := cmdIdentityProviderGroup{global: c.global}
	cmd.AddCommand(identityProviderGroupCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

type cmdGroup struct {
	global *cmdGlobal
}

func (c *cmdGroup) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("group")
	cmd.Short = i18n.G("Manage groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage groups`))

	groupCreateCmd := cmdGroupCreate{global: c.global}
	cmd.AddCommand(groupCreateCmd.command())

	groupDeleteCmd := cmdGroupDelete{global: c.global}
	cmd.AddCommand(groupDeleteCmd.command())

	groupEditCmd := cmdGroupEdit{global: c.global}
	cmd.AddCommand(groupEditCmd.command())

	groupShowCmd := cmdGroupShow{global: c.global}
	cmd.AddCommand(groupShowCmd.command())

	groupListCmd := cmdGroupList{global: c.global}
	cmd.AddCommand(groupListCmd.command())

	groupRenameCmd := cmdGroupRename{global: c.global}
	cmd.AddCommand(groupRenameCmd.command())

	permissionCmd := cmdGroupPermission{global: c.global}
	cmd.AddCommand(permissionCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

type cmdGroupCreate struct {
	global          *cmdGlobal
	flagDescription string
}

func (c *cmdGroupCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<group>"))
	cmd.Short = i18n.G("Create groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create groups`))
	cmd.Flags().StringVarP(&c.flagDescription, "description", "d", "", "Group description")
	cmd.RunE = c.run

	return cmd
}

func (c *cmdGroupCreate) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing group name"))
	}

	// Create the group
	group := api.AuthGroupsPost{}
	group.Name = resource.name
	group.Description = c.flagDescription

	err = resource.server.CreateAuthGroup(group)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Group %s created")+"\n", resource.name)
	}

	return nil
}

// Delete.
type cmdGroupDelete struct {
	global *cmdGlobal
}

func (c *cmdGroupDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<group>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete groups`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdGroupDelete) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing group name"))
	}

	// Delete the group
	err = resource.server.DeleteAuthGroup(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Group %s deleted")+"\n", resource.name)
	}

	return nil
}

// Edit.
type cmdGroupEdit struct {
	global *cmdGlobal
}

func (c *cmdGroupEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<group>"))
	cmd.Short = i18n.G("Edit groups as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit groups as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc auth group edit <group> < group.yaml
   Update a group using the content of group.yaml`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdGroupEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the group.
### Any line starting with a '# will be ignored.
###
### A group has the following format:
### name: my-first-group
### description: My first group.
### permissions:
### - entity_type: project
###   url: /1.0/projects/default
###   entitlement: can_view
### identities:
### - authentication_method: oidc
###   type: OIDC client
###   identifier: jane.doe@example.com
###   name: Jane Doe
###   metadata:
###     subject: auth0|123456789
### identity_provider_groups:
### - sales
### - operations
###
### Note that all group information is shown but only the description and permissions can be modified`)
}

func (c *cmdGroupEdit) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing group name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.AuthGroupPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateAuthGroup(resource.name, newdata, "")
	}

	// Extract the current value
	group, etag, err := resource.server.GetAuthGroup(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&group)
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
		newdata := api.AuthGroupPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateAuthGroup(resource.name, newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Could not parse group: %s")+"\n", err)
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

type cmdGroupList struct {
	global     *cmdGlobal
	flagFormat string
}

func (c *cmdGroupList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List groups`))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	return cmd
}

func (c *cmdGroupList) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// List groups
	groups, err := resource.server.GetAuthGroups()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, group := range groups {
		data = append(data, []string{group.Name, group.Description})
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
	}

	return cli.RenderTable(c.flagFormat, header, data, groups)
}

// Rename.
type cmdGroupRename struct {
	global *cmdGlobal
}

func (c *cmdGroupRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<group> <new_name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename groups`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdGroupRename) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing group name"))
	}

	// Rename the group
	err = resource.server.RenameAuthGroup(resource.name, api.AuthGroupPost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Group %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Show.
type cmdGroupShow struct {
	global *cmdGlobal
}

func (c *cmdGroupShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<group>"))
	cmd.Short = i18n.G("Show group configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show group configurations`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdGroupShow) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing group name"))
	}

	// Show the group
	group, _, err := resource.server.GetAuthGroup(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&group)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

type cmdGroupPermission struct {
	global *cmdGlobal
}

func (c *cmdGroupPermission) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("permission")
	cmd.Aliases = []string{"perm"}
	cmd.Short = i18n.G("Manage permissions")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage permissions`))

	groupCreateCmd := cmdGroupPermissionAdd{global: c.global}
	cmd.AddCommand(groupCreateCmd.command())

	groupDeleteCmd := cmdGroupPermissionRemove{global: c.global}
	cmd.AddCommand(groupDeleteCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

type cmdGroupPermissionAdd struct {
	global *cmdGlobal
}

func (c *cmdGroupPermissionAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<group> <entity_type> [<entity_name>] <entitlement> [<key>=<value>...]"))
	cmd.Short = i18n.G("Add permissions to groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add permissions to groups`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdGroupPermissionAdd) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing group name"))
	}

	group, eTag, err := resource.server.GetAuthGroup(resource.name)
	if err != nil {
		return err
	}

	permission, err := parsePermissionArgs(args)
	if err != nil {
		return err
	}

	added := false
	if !shared.ValueInSlice(*permission, group.Permissions) {
		group.Permissions = append(group.Permissions, *permission)
		added = true
	}

	if !added {
		return fmt.Errorf("Group %q already has entitlement %q on entity %q", resource.name, permission.Entitlement, permission.EntityReference)
	}

	return resource.server.UpdateAuthGroup(resource.name, group.Writable(), eTag)
}

type cmdGroupPermissionRemove struct {
	global *cmdGlobal
}

func (c *cmdGroupPermissionRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<group> <entity_type> [<entity_name>] <entitlement> [<key>=<value>...]"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Remove permissions from groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove permissions from groups`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdGroupPermissionRemove) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing group name"))
	}

	group, eTag, err := resource.server.GetAuthGroup(resource.name)
	if err != nil {
		return err
	}

	permission, err := parsePermissionArgs(args)
	if err != nil {
		return err
	}

	if len(group.Permissions) == 0 {
		return fmt.Errorf("Group %q does not have any permissions", resource.name)
	}

	permissions := make([]api.Permission, 0, len(group.Permissions)-1)
	removed := false
	for _, existingPermission := range group.Permissions {
		if *permission == existingPermission {
			removed = true
			continue
		}

		permissions = append(permissions, existingPermission)
	}

	if !removed {
		return fmt.Errorf("Group %q does not have entitlement %q on entity %q", resource.name, permission.Entitlement, permission.EntityReference)
	}

	group.Permissions = permissions
	return resource.server.UpdateAuthGroup(resource.name, group.Writable(), eTag)
}

// parsePermissionArgs parses the `<entity_type> [<entity_name>] <entitlement> [<key>=<value>...]` arguments of
// `lxc auth group permission add/remove` and returns an api.Permission that can be appended/removed from the list of
// permissions belonging to a group.
func parsePermissionArgs(args []string) (*api.Permission, error) {
	entityType := entity.Type(args[1])
	err := entityType.Validate()
	if err != nil {
		return nil, err
	}

	if entityType == entity.TypeServer {
		if len(args) != 3 {
			return nil, fmt.Errorf("Expected three arguments: `lxc auth group grant [<remote>:]<group> server <entitlement>`")
		}

		return &api.Permission{
			EntityType:      string(entityType),
			EntityReference: entity.ServerURL().String(),
			Entitlement:     args[2],
		}, nil
	}

	if len(args) < 4 {
		return nil, fmt.Errorf("Expected at least four arguments: `lxc auth group grant [<remote>:]<group> <object_type> <object_name> <entitlement> [<key>=<value>...]`")
	}

	entityName := args[2]
	entitlement := args[3]

	kv := make(map[string]string)
	if len(args) > 4 {
		for _, arg := range args[4:] {
			k, v, ok := strings.Cut(arg, "=")
			if !ok {
				return nil, fmt.Errorf("Supplementary arguments must be of the form <key>=<value>")
			}

			kv[k] = v
		}
	}

	pathArgs := []string{entityName}
	if entityType == entity.TypeIdentity {
		authenticationMethod, identifier, ok := strings.Cut(entityName, "/")
		if !ok {
			return nil, fmt.Errorf("Malformed identity argument, expected `<authentication_method>/<identifier>`, got %q", entityName)
		}

		pathArgs = []string{authenticationMethod, identifier}
	}

	projectName, ok := kv["project"]
	requiresProject, _ := entityType.RequiresProject()
	if requiresProject && !ok {
		return nil, fmt.Errorf("Entities of type %q require a supplementary project argument `project=<project_name>`", entityType)
	}

	if entityType == entity.TypeStorageVolume {
		storageVolumeType, ok := kv["type"]
		if !ok {
			return nil, fmt.Errorf("Entities of type %q require a supplementary storage volume type argument `type=<storage volume type>`", entityType)
		}

		pathArgs = append([]string{storageVolumeType}, pathArgs...)
	}

	if entityType == entity.TypeStorageVolume || entityType == entity.TypeStorageBucket {
		storagePool, ok := kv["pool"]
		if !ok {
			return nil, fmt.Errorf("Entities of type %q require a supplementary storage pool argument `pool=<pool_name>`", entityType)
		}

		pathArgs = append([]string{storagePool}, pathArgs...)
	}

	entityURL, err := entityType.URL(projectName, kv["location"], pathArgs...)
	if err != nil {
		return nil, err
	}

	return &api.Permission{
		EntityType:      string(entityType),
		EntityReference: entityURL.String(),
		Entitlement:     entitlement,
	}, nil
}

type cmdIdentity struct {
	global *cmdGlobal
}

func (c *cmdIdentity) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("identity")
	cmd.Aliases = []string{"user"}
	cmd.Short = i18n.G("Manage identities")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage identities`))

	identityListCmd := cmdIdentityList{global: c.global}
	cmd.AddCommand(identityListCmd.command())

	identityShowCmd := cmdIdentityShow{global: c.global}
	cmd.AddCommand(identityShowCmd.command())

	identityInfoCmd := cmdIdentityInfo{global: c.global}
	cmd.AddCommand(identityInfoCmd.command())

	identityEditCmd := cmdIdentityEdit{global: c.global}
	cmd.AddCommand(identityEditCmd.command())

	identityGroupCmd := cmdIdentityGroup{global: c.global}
	cmd.AddCommand(identityGroupCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

type cmdIdentityList struct {
	global     *cmdGlobal
	flagFormat string
}

func (c *cmdIdentityList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List identities")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List identities`))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	return cmd
}

func (c *cmdIdentityList) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// List identities
	identities, err := resource.server.GetIdentities()
	if err != nil {
		return err
	}

	data := [][]string{}
	delimiter := "\n"
	if c.flagFormat == cli.TableFormatCSV {
		delimiter = ","
	}

	for _, identity := range identities {
		data = append(data, []string{identity.AuthenticationMethod, identity.Type, identity.Name, identity.Identifier, strings.Join(identity.Groups, delimiter)})
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("AUTHENTICATION METHOD"),
		i18n.G("TYPE"),
		i18n.G("NAME"),
		i18n.G("IDENTIFIER"),
		i18n.G("GROUPS"),
	}

	return cli.RenderTable(c.flagFormat, header, data, identities)
}

// Show.
type cmdIdentityShow struct {
	global *cmdGlobal
}

func (c *cmdIdentityShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<authentication_method>/<name_or_identifier>"))
	cmd.Short = i18n.G("View an identity")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show identity configurations

The argument must be a concatenation of the authentication method and either the
name or identifier of the identity, delimited by a forward slash. This command
will fail if an identity name is used that is not unique within the authentication
method. Use the identifier instead if this occurs.
`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdIdentityShow) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing identity argument"))
	}

	authenticationMethod, nameOrID, ok := strings.Cut(resource.name, "/")
	if !ok {
		return fmt.Errorf("Malformed argument, expected `[<remote>:]<authentication_method>/<name_or_identifier>`, got %q", args[0])
	}

	// Show the identity
	identity, _, err := resource.server.GetIdentity(authenticationMethod, nameOrID)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&identity)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Show current.
type cmdIdentityInfo struct {
	global *cmdGlobal
}

func (c *cmdIdentityInfo) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", i18n.G("[<remote>:]"))
	cmd.Short = i18n.G("View the current identity")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show the current identity

This command will display permissions for the current user.
This includes contextual information, such as effective groups and permissions
that are granted via identity provider group mappings. 
`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdIdentityInfo) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Connect to LXD
	var server lxd.InstanceServer
	if len(args) == 0 {
		server, err = c.global.conf.GetInstanceServer(c.global.conf.DefaultRemote)
		if err != nil {
			return err
		}
	} else {
		resources, err := c.global.ParseServers(args[0])
		if err != nil {
			return err
		}

		server = resources[0].server
	}

	// Show the identity
	identity, _, err := server.GetCurrentIdentityInfo()
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&identity)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Edit.
type cmdIdentityEdit struct {
	global *cmdGlobal
}

func (c *cmdIdentityEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<group>"))
	cmd.Short = i18n.G("Edit an identity as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit an identity as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc auth identity edit <authentication_method>/<name_or_identifier> < identity.yaml
   Update an identity using the content of identity.yaml`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdIdentityEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the group.
### Any line starting with a '# will be ignored.
###
### An identity has the following format:
### authentication_method: oidc
### type: OIDC client
### identifier: jane.doe@example.com
### name: Jane Doe
### metadata:
###   subject: auth0|123456789
### projects:
### - default
### groups:
### - my-first-group
###
### Note that all identity information is shown but only the projects and groups can be modified`)
}

func (c *cmdIdentityEdit) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing identity argument"))
	}

	authenticationMethod, nameOrID, ok := strings.Cut(resource.name, "/")
	if !ok {
		return fmt.Errorf("Malformed argument, expected `[<remote>:]<authentication_method>/<name_or_identifier>`, got %q", args[0])
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.IdentityPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateIdentity(authenticationMethod, nameOrID, newdata, "")
	}

	// Extract the current value
	identity, etag, err := resource.server.GetIdentity(authenticationMethod, nameOrID)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&identity)
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
		newdata := api.IdentityPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateIdentity(authenticationMethod, nameOrID, newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Could not parse identity: %s")+"\n", err)
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

type cmdIdentityGroup struct {
	global *cmdGlobal
}

func (c *cmdIdentityGroup) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("group")
	cmd.Short = i18n.G("Manage groups for the identity")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage groups for the identity`))

	identityGroupAddCmd := cmdIdentityGroupAdd{global: c.global}
	cmd.AddCommand(identityGroupAddCmd.command())

	identityGroupRemoveCmd := cmdIdentityGroupRemove{global: c.global}
	cmd.AddCommand(identityGroupRemoveCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

type cmdIdentityGroupAdd struct {
	global *cmdGlobal
}

func (c *cmdIdentityGroupAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<authentication_method>/<name_or_identifier> <group>"))
	cmd.Short = i18n.G("Add a group to an identity")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add a group to an identity`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdIdentityGroupAdd) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing identity argument"))
	}

	authenticationMethod, nameOrID, ok := strings.Cut(resource.name, "/")
	if !ok {
		return fmt.Errorf("Malformed argument, expected `[<remote>:]<authentication_method>/<name_or_identifier>`, got %q", args[0])
	}

	identity, eTag, err := resource.server.GetIdentity(authenticationMethod, nameOrID)
	if err != nil {
		return err
	}

	added := false
	if !shared.ValueInSlice(args[1], identity.Groups) {
		identity.Groups = append(identity.Groups, args[1])
		added = true
	}

	if !added {
		return fmt.Errorf("Identity %q is already a member of group %q", resource.name, args[1])
	}

	return resource.server.UpdateIdentity(authenticationMethod, nameOrID, identity.Writable(), eTag)
}

type cmdIdentityGroupRemove struct {
	global *cmdGlobal
}

func (c *cmdIdentityGroupRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<authentication_method>/<name_or_identifier> <group>"))
	cmd.Short = i18n.G("Remove a group from an identity")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove a group from an identity`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdIdentityGroupRemove) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing identity argument"))
	}

	authenticationMethod, nameOrID, ok := strings.Cut(resource.name, "/")
	if !ok {
		return fmt.Errorf("Malformed argument, expected `[<remote>:]<authentication_method>/<name_or_identifier>`, got %q", args[0])
	}

	identity, eTag, err := resource.server.GetIdentity(authenticationMethod, nameOrID)
	if err != nil {
		return err
	}

	if len(identity.Groups) == 0 {
		return fmt.Errorf("Identity %q is not a member of any groups", resource.name)
	}

	groups := make([]string, 0, len(identity.Groups)-1)
	removed := false
	for _, existingGroup := range identity.Groups {
		if args[1] == existingGroup {
			removed = true
			continue
		}

		groups = append(groups, existingGroup)
	}

	if !removed {
		return fmt.Errorf("Identity %q is not a member of group %q", resource.name, args[0])
	}

	identity.Groups = groups
	return resource.server.UpdateIdentity(authenticationMethod, nameOrID, identity.Writable(), eTag)
}

type cmdPermission struct {
	global *cmdGlobal
}

func (c *cmdPermission) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("permission")
	cmd.Aliases = []string{"perm"}
	cmd.Short = i18n.G("Inspect permissions")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Inspect permissions`))

	permissionListCmd := cmdPermissionList{global: c.global}
	cmd.AddCommand(permissionListCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

type cmdPermissionList struct {
	global              *cmdGlobal
	flagMaxEntitlements int
	flagFormat          string
}

func (c *cmdPermissionList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:] [project=<project_name>] [entity_type=<entity_type>]"))
	cmd.Short = i18n.G("List permissions")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List permissions`))

	cmd.Flags().IntVar(&c.flagMaxEntitlements, "max-entitlements", 3, "Maximum number of unassigned entitlements to display before overflowing (set to zero to display all)")
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", cli.TableFormatTable, "Display format (json, yaml, table, compact, csv)")
	cmd.RunE = c.run

	return cmd
}

func (c *cmdPermissionList) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 3)
	if exit {
		return err
	}

	filters := args
	remote := c.global.conf.DefaultRemote

	// If there are arguments, and first argument contains a colon and does not contain an equals, use it as the remote name.
	if len(args) > 0 && strings.Contains(args[0], ":") && !strings.Contains(args[0], "=") {
		var err error
		remote, _, err = c.global.conf.ParseRemote(args[0])
		if err != nil {
			return err
		}

		filters = args[1:]
	}

	client, err := c.global.conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	projectName := ""
	entityType := entity.Type("")
	for _, filter := range filters {
		k, v, ok := strings.Cut(filter, "=")
		if !ok {
			return fmt.Errorf("Badly formatted supplementary argument %q", filter)
		}

		if k == "project" {
			projectName = v
		} else if k == "entity_type" {
			entityType = entity.Type(v)
			err = entityType.Validate()
			if err != nil {
				return fmt.Errorf("Invalid entity type in supplementary argument %q: %w", filter, err)
			}
		} else {
			return fmt.Errorf("Available filters are `entity_type` and `project`, got %q", filter)
		}
	}

	permissionsInfo, err := client.GetPermissionsInfo(lxd.GetPermissionsArgs{
		EntityType:  string(entityType),
		ProjectName: projectName,
	})
	if err != nil {
		return err
	}

	// If we're displaying with JSON or YAML, display the raw data now.
	if c.flagFormat == cli.TableFormatJSON || c.flagFormat == cli.TableFormatYAML {
		return cli.RenderTable(c.flagFormat, nil, nil, permissionsInfo)
	}

	// Otherwise, data returned from the permissions API can be condensed into a more easily viewable format.
	// We'll group entitlements together by the API resource they are defined on, and separate the entitlements that
	// are assigned to groups from the ones that are not assigned.
	type displayPermission struct {
		entityType              string
		url                     string
		entitlementsAssigned    map[string][]string
		entitlementsNotAssigned []string
	}

	i := 0
	var displayPermissions []*displayPermission
	displayPermissionIdx := make(map[string]int)
	for _, perm := range permissionsInfo {
		idx, ok := displayPermissionIdx[perm.EntityReference]
		if ok {
			dp := displayPermissions[idx]
			if len(perm.Groups) > 0 {
				dp.entitlementsAssigned[perm.Entitlement] = perm.Groups
			} else {
				dp.entitlementsNotAssigned = append(dp.entitlementsNotAssigned, perm.Entitlement)
			}

			continue
		}

		dp := displayPermission{
			entityType:           perm.EntityType,
			url:                  perm.EntityReference,
			entitlementsAssigned: make(map[string][]string),
		}

		if len(perm.Groups) > 0 {
			dp.entitlementsAssigned[perm.Entitlement] = perm.Groups
		} else {
			dp.entitlementsNotAssigned = append(dp.entitlementsNotAssigned, perm.Entitlement)
		}

		displayPermissions = append(displayPermissions, &dp)
		displayPermissionIdx[perm.EntityReference] = i
		i++
	}

	columns := map[rune]cli.Column{
		't': {
			Header: "ENTITY TYPE",
			DataFunc: func(a any) (string, error) {
				p, _ := a.(*displayPermission)
				return p.entityType, nil
			},
		},
		'u': {
			Header: "URL",
			DataFunc: func(a any) (string, error) {
				p, _ := a.(*displayPermission)
				return p.url, nil
			},
		},
		'e': {
			Header: "ENTITLEMENTS ==> (GROUPS)",
			DataFunc: func(a any) (string, error) {
				p, _ := a.(*displayPermission)
				var rowsAssigned []string
				for k, v := range p.entitlementsAssigned {
					// Pretty format for tables.
					assignedRow := fmt.Sprintf("%s ==> (%s)", k, strings.Join(v, ", "))
					if c.flagFormat == cli.TableFormatCSV {
						// Machine readable format for CSV.
						assignedRow = fmt.Sprintf("%s:(%s)", k, strings.Join(v, ","))
					}

					rowsAssigned = append(rowsAssigned, assignedRow)
				}

				// Sort the entitlements alphabetically, and put the assigned entitlements first.
				sort.Strings(rowsAssigned)
				sort.Strings(p.entitlementsNotAssigned)

				// Only show unassigned entitlements up to and including `--max-entitlements`
				if c.flagMaxEntitlements > 0 && len(p.entitlementsNotAssigned) > c.flagMaxEntitlements {
					p.entitlementsNotAssigned = p.entitlementsNotAssigned[:c.flagMaxEntitlements]
					p.entitlementsNotAssigned = append(p.entitlementsNotAssigned[:c.flagMaxEntitlements], "...")
				}

				rows := append(rowsAssigned, p.entitlementsNotAssigned...)
				delimiter := "\n"
				if c.flagFormat == cli.TableFormatCSV {
					// Don't use newlines for CSV. We can use a comma because the field will be wrapped in quotes.
					delimiter = ","
				}

				return strings.Join(rows, delimiter), nil
			},
		},
	}

	return cli.RenderSlice(displayPermissions, c.flagFormat, "tue", "u", columns)
}

type cmdIdentityProviderGroup struct {
	global *cmdGlobal
}

func (c *cmdIdentityProviderGroup) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("identity-provider-group")
	cmd.Aliases = []string{"idp-group"}
	cmd.Short = i18n.G("Manage groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage groups`))

	idpGroupCreateCmd := cmdIdentityProviderGroupCreate{global: c.global}
	cmd.AddCommand(idpGroupCreateCmd.command())

	idpGroupDeleteCmd := cmdIdentityProviderGroupDelete{global: c.global}
	cmd.AddCommand(idpGroupDeleteCmd.command())

	idpGroupEditCmd := cmdIdentityProviderGroupEdit{global: c.global}
	cmd.AddCommand(idpGroupEditCmd.command())

	idpGroupShowCmd := cmdIdentityProviderGroupShow{global: c.global}
	cmd.AddCommand(idpGroupShowCmd.command())

	idpGroupListCmd := cmdIdentityProviderGroupList{global: c.global}
	cmd.AddCommand(idpGroupListCmd.command())

	idpGroupRenameCmd := cmdIdentityProviderGroupRename{global: c.global}
	cmd.AddCommand(idpGroupRenameCmd.command())

	idpGroupGroupCmd := cmdIdentityProviderGroupGroup{global: c.global}
	cmd.AddCommand(idpGroupGroupCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

type cmdIdentityProviderGroupCreate struct {
	global *cmdGlobal
}

func (c *cmdIdentityProviderGroupCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<group>"))
	cmd.Short = i18n.G("Create identity provider groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create identity provider groups`))
	cmd.RunE = c.run

	return cmd
}

func (c *cmdIdentityProviderGroupCreate) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing identity provider group name"))
	}

	// Create the identity provider group
	group := api.IdentityProviderGroup{}
	group.Name = resource.name

	err = resource.server.CreateIdentityProviderGroup(group)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Identity provider group %s created")+"\n", resource.name)
	}

	return nil
}

// Delete.
type cmdIdentityProviderGroupDelete struct {
	global *cmdGlobal
}

func (c *cmdIdentityProviderGroupDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<identity_provider_group>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete identity provider groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete identity provider groups`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdIdentityProviderGroupDelete) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing identity provider group name"))
	}

	// Delete the identity provider group
	err = resource.server.DeleteIdentityProviderGroup(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Identity provider group %s deleted")+"\n", resource.name)
	}

	return nil
}

// Edit.
type cmdIdentityProviderGroupEdit struct {
	global *cmdGlobal
}

func (c *cmdIdentityProviderGroupEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<identity_provider_group>"))
	cmd.Short = i18n.G("Edit identity provider groups as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit identity provider groups as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc auth identity-provider-group edit <identity_provider_group> < identity-provider-group.yaml
   Update an identity provider group using the content of identity-provider-group.yaml`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdIdentityProviderGroupEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the identity provider group.
### Any line starting with a '# will be ignored.
###
### An identity provider group has the following format:
### name: operations
### groups:
### - foo
### - bar
###
### Note that the name is shown but cannot be modified`)
}

func (c *cmdIdentityProviderGroupEdit) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing identity provider group name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.IdentityProviderGroupPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateIdentityProviderGroup(resource.name, newdata, "")
	}

	// Extract the current value
	group, etag, err := resource.server.GetIdentityProviderGroup(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&group)
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
		newdata := api.IdentityProviderGroupPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateIdentityProviderGroup(resource.name, newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Could not parse group: %s")+"\n", err)
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

type cmdIdentityProviderGroupList struct {
	global     *cmdGlobal
	flagFormat string
}

func (c *cmdIdentityProviderGroupList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List identity provider groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List identity provider groups`))

	cmd.RunE = c.run
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	return cmd
}

func (c *cmdIdentityProviderGroupList) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// List identity provider groups
	groups, err := resource.server.GetIdentityProviderGroups()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, group := range groups {
		data = append(data, []string{group.Name, strings.Join(group.Groups, "\n")})
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("GROUPS"),
	}

	return cli.RenderTable(c.flagFormat, header, data, groups)
}

// Rename.
type cmdIdentityProviderGroupRename struct {
	global *cmdGlobal
}

func (c *cmdIdentityProviderGroupRename) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("rename", i18n.G("[<remote>:]<identity_provider_group> <new_name>"))
	cmd.Aliases = []string{"mv"}
	cmd.Short = i18n.G("Rename identity provider groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Rename identity provider groups`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdIdentityProviderGroupRename) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing identity provider group name"))
	}

	// Rename the group
	err = resource.server.RenameIdentityProviderGroup(resource.name, api.IdentityProviderGroupPost{Name: args[1]})
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Group %s renamed to %s")+"\n", resource.name, args[1])
	}

	return nil
}

// Show.
type cmdIdentityProviderGroupShow struct {
	global *cmdGlobal
}

func (c *cmdIdentityProviderGroupShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<identity_provider_group>"))
	cmd.Short = i18n.G("Show an identity provider group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show an identity provider group`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdIdentityProviderGroupShow) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing group name"))
	}

	// Show the group
	group, _, err := resource.server.GetIdentityProviderGroup(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&group)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

type cmdIdentityProviderGroupGroup struct {
	global *cmdGlobal
}

func (c *cmdIdentityProviderGroupGroup) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("group")
	cmd.Short = i18n.G("Manage identity provider group mappings")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage identity provider group mappings`))

	identityProviderGroupGroupAddCmd := cmdIdentityProviderGroupGroupAdd{global: c.global}
	cmd.AddCommand(identityProviderGroupGroupAddCmd.command())

	identityProviderGroupGroupRemoveCmd := cmdIdentityProviderGroupGroupRemove{global: c.global}
	cmd.AddCommand(identityProviderGroupGroupRemoveCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

type cmdIdentityProviderGroupGroupAdd struct {
	global *cmdGlobal
}

func (c *cmdIdentityProviderGroupGroupAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<identity_provider_group> <group>"))
	cmd.Short = i18n.G("Add a group to an identity provider group")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add a group to an identity provider group`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdIdentityProviderGroupGroupAdd) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing identity provider group name argument"))
	}

	idpGroup, eTag, err := resource.server.GetIdentityProviderGroup(resource.name)
	if err != nil {
		return err
	}

	added := false
	if !shared.ValueInSlice(args[1], idpGroup.Groups) {
		idpGroup.Groups = append(idpGroup.Groups, args[1])
		added = true
	}

	if !added {
		return fmt.Errorf("Identity group %q is already mapped to group %q", resource.name, args[1])
	}

	return resource.server.UpdateIdentityProviderGroup(resource.name, idpGroup.Writable(), eTag)
}

type cmdIdentityProviderGroupGroupRemove struct {
	global *cmdGlobal
}

func (c *cmdIdentityProviderGroupGroupRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<authentication_method>/<name_or_identifier> <group>"))
	cmd.Short = i18n.G("Remove identities from groups")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove identities from groups`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdIdentityProviderGroupGroupRemove) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing identity provider group name argument"))
	}

	idpGroup, eTag, err := resource.server.GetIdentityProviderGroup(resource.name)
	if err != nil {
		return err
	}

	if len(idpGroup.Groups) == 0 {
		return fmt.Errorf("Identity provider group %q is not mapped to any groups", resource.name)
	}

	groups := make([]string, 0, len(idpGroup.Groups)-1)
	removed := false
	for _, existingGroup := range idpGroup.Groups {
		if args[1] == existingGroup {
			removed = true
			continue
		}

		groups = append(groups, existingGroup)
	}

	if !removed {
		return fmt.Errorf("Identity provider group %q is not mapped to group %q", resource.name, args[1])
	}

	idpGroup.Groups = groups
	return resource.server.UpdateIdentityProviderGroup(resource.name, idpGroup.Writable(), eTag)
}
