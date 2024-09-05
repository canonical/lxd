package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
	"github.com/canonical/lxd/shared/termios"
	"github.com/canonical/lxd/shared/units"
)

type cmdStorage struct {
	global *cmdGlobal

	flagTarget string
}

func (c *cmdStorage) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("storage")
	cmd.Short = i18n.G("Manage storage pools and volumes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage storage pools and volumes`))

	// Create
	storageCreateCmd := cmdStorageCreate{global: c.global, storage: c}
	cmd.AddCommand(storageCreateCmd.command())

	// Delete
	storageDeleteCmd := cmdStorageDelete{global: c.global, storage: c}
	cmd.AddCommand(storageDeleteCmd.command())

	// Edit
	storageEditCmd := cmdStorageEdit{global: c.global, storage: c}
	cmd.AddCommand(storageEditCmd.command())

	// Get
	storageGetCmd := cmdStorageGet{global: c.global, storage: c}
	cmd.AddCommand(storageGetCmd.command())

	// Info
	storageInfoCmd := cmdStorageInfo{global: c.global, storage: c}
	cmd.AddCommand(storageInfoCmd.command())

	// List
	storageListCmd := cmdStorageList{global: c.global, storage: c}
	cmd.AddCommand(storageListCmd.command())

	// Set
	storageSetCmd := cmdStorageSet{global: c.global, storage: c}
	cmd.AddCommand(storageSetCmd.command())

	// Show
	storageShowCmd := cmdStorageShow{global: c.global, storage: c}
	cmd.AddCommand(storageShowCmd.command())

	// Unset
	storageUnsetCmd := cmdStorageUnset{global: c.global, storage: c, storageSet: &storageSetCmd}
	cmd.AddCommand(storageUnsetCmd.command())

	// Bucket
	storageBucketCmd := cmdStorageBucket{global: c.global}
	cmd.AddCommand(storageBucketCmd.command())

	// Volume
	storageVolumeCmd := cmdStorageVolume{global: c.global, storage: c}
	cmd.AddCommand(storageVolumeCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdStorageCreate struct {
	global  *cmdGlobal
	storage *cmdStorage
}

func (c *cmdStorageCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<pool> <driver> [key=value...]"))
	cmd.Short = i18n.G("Create storage pools")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create storage pools`))
	cmd.Example = cli.FormatSection("", i18n.G(`lxc storage create s1 dir

lxc storage create s1 dir < config.yaml
    Create a storage pool using the content of config.yaml.
	`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageCreate) run(cmd *cobra.Command, args []string) error {
	var stdinData api.StoragePoolPut

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		err = yaml.Unmarshal(contents, &stdinData)
		if err != nil {
			return err
		}
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	client := resource.server

	// Create the new storage pool entry
	pool := api.StoragePoolsPost{
		Name:           resource.name,
		Driver:         args[1],
		StoragePoolPut: stdinData,
	}

	if pool.Config == nil {
		pool.Config = map[string]string{}
		for i := 2; i < len(args); i++ {
			entry := strings.SplitN(args[i], "=", 2)
			if len(entry) < 2 {
				return fmt.Errorf(i18n.G("Bad key=value pair: %s"), entry)
			}

			pool.Config[entry[0]] = entry[1]
		}
	}

	// If a target member was specified the API won't actually create the
	// pool, but only define it as pending in the database.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	// Create the pool
	err = client.CreateStoragePool(pool)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		if c.storage.flagTarget != "" {
			fmt.Printf(i18n.G("Storage pool %s pending on member %s")+"\n", resource.name, c.storage.flagTarget)
		} else {
			fmt.Printf(i18n.G("Storage pool %s created")+"\n", resource.name)
		}
	}

	return nil
}

// Delete.
type cmdStorageDelete struct {
	global  *cmdGlobal
	storage *cmdStorage
}

func (c *cmdStorageDelete) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<pool>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete storage pools")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete storage pools`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageDelete) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing pool name"))
	}

	// Delete the pool
	err = resource.server.DeleteStoragePool(resource.name)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Storage pool %s deleted")+"\n", resource.name)
	}

	return nil
}

// Edit.
type cmdStorageEdit struct {
	global  *cmdGlobal
	storage *cmdStorage
}

func (c *cmdStorageEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<pool>"))
	cmd.Short = i18n.G("Edit storage pool configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit storage pool configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc storage edit [<remote>:]<pool> < pool.yaml
    Update a storage pool using the content of pool.yaml.`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of a storage pool.
### Any line starting with a '#' will be ignored.
###
### A storage pool consists of a set of configuration items.
###
### An example would look like:
### name: default
### driver: zfs
### used_by: []
### config:
###   size: "61203283968"
###   source: /home/chb/mnt/lxd_test/default.img
###   zfs.pool_name: default`)
}

func (c *cmdStorageEdit) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing pool name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.StoragePoolPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateStoragePool(resource.name, newdata, "")
	}

	// Extract the current value
	pool, etag, err := resource.server.GetStoragePool(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&pool)
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
		newdata := api.StoragePoolPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateStoragePool(resource.name, newdata, etag)
		}

		// Respawn the editor
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

// Get.
type cmdStorageGet struct {
	global  *cmdGlobal
	storage *cmdStorage

	flagIsProperty bool
}

func (c *cmdStorageGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<pool> <key>"))
	cmd.Short = i18n.G("Get values for storage pool configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for storage pool configuration keys`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Get the key as a storage property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageGet) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing pool name"))
	}

	// If a target member was specified, we return also member-specific config values.
	if c.storage.flagTarget != "" {
		resource.server = resource.server.UseTarget(c.storage.flagTarget)
	}

	// Get the property
	resp, _, err := resource.server.GetStoragePool(resource.name)
	if err != nil {
		return err
	}

	if c.flagIsProperty {
		w := resp.Writable()
		res, err := getFieldByJsonTag(&w, args[1])
		if err != nil {
			return fmt.Errorf(i18n.G("The property %q does not exist on the storage pool %q: %v"), args[1], resource.name, err)
		}

		fmt.Printf("%v\n", res)
	} else {
		v, ok := resp.Config[args[1]]
		if ok {
			fmt.Println(v)
		}
	}

	return nil
}

// Info.
type cmdStorageInfo struct {
	global  *cmdGlobal
	storage *cmdStorage

	flagBytes bool
}

func (c *cmdStorageInfo) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", i18n.G("[<remote>:]<pool>"))
	cmd.Short = i18n.G("Show useful information about storage pools")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show useful information about storage pools`))

	cmd.Flags().BoolVar(&c.flagBytes, "bytes", false, i18n.G("Show the used and free space in bytes"))
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageInfo) run(cmd *cobra.Command, args []string) error {
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
		return errors.New(i18n.G("Missing pool name"))
	}

	// Targeting
	if c.storage.flagTarget != "" {
		if !resource.server.IsClustered() {
			return errors.New(i18n.G("To use --target, the destination remote must be a cluster"))
		}

		resource.server = resource.server.UseTarget(c.storage.flagTarget)
	}

	// Get the pool information
	pool, _, err := resource.server.GetStoragePool(resource.name)
	if err != nil {
		return err
	}

	res, err := resource.server.GetStoragePoolResources(resource.name)
	if err != nil {
		return err
	}

	// Declare the poolinfo map of maps in order to build up the yaml
	poolinfo := make(map[string]map[string]string)
	poolusedby := make(map[string]map[string][]string)

	// Translations
	usedbystring := i18n.G("used by")
	infostring := i18n.G("info")
	namestring := i18n.G("name")
	driverstring := i18n.G("driver")
	descriptionstring := i18n.G("description")
	totalspacestring := i18n.G("total space")
	spaceusedstring := i18n.G("space used")

	// Initialize the usedby map
	poolusedby[usedbystring] = make(map[string][]string)

	// Build up the usedby map
	for _, v := range pool.UsedBy {
		u, err := url.Parse(v)
		if err != nil {
			continue
		}

		fields := strings.Split(strings.TrimPrefix(u.Path, "/1.0/"), "/")
		fieldsLen := len(fields)

		entityType := "unrecognized"
		entityName := u.Path

		if fieldsLen > 1 {
			entityType = fields[0]
			entityName = fields[1]

			if fields[fieldsLen-2] == "snapshots" {
				continue // Skip snapshots as the parent entity will be included once in the list.
			}

			if fields[0] == "storage-pools" && fieldsLen > 3 {
				entityType = fields[2]
				entityName = fields[3]

				if entityType == "volumes" && fieldsLen > 4 {
					entityName = fields[4]
				}
			}
		}

		var sb strings.Builder
		var attribs []string
		sb.WriteString(entityName)

		// Show info regarding the project and location if present.
		values := u.Query()
		projectName := values.Get("project")
		if projectName != "" {
			attribs = append(attribs, fmt.Sprintf("project %q", projectName))
		}

		locationName := values.Get("target")
		if locationName != "" {
			attribs = append(attribs, fmt.Sprintf("location %q", locationName))
		}

		if len(attribs) > 0 {
			sb.WriteString(" (")
			for i, attrib := range attribs {
				if i > 0 {
					sb.WriteString(", ")
				}

				sb.WriteString(attrib)
			}

			sb.WriteString(")")
		}

		poolusedby[usedbystring][entityType] = append(poolusedby[usedbystring][entityType], sb.String())
	}

	// Initialize the info map
	poolinfo[infostring] = map[string]string{}

	// Build up the info map
	poolinfo[infostring][namestring] = pool.Name
	poolinfo[infostring][driverstring] = pool.Driver
	poolinfo[infostring][descriptionstring] = pool.Description
	if c.flagBytes {
		poolinfo[infostring][totalspacestring] = strconv.FormatUint(res.Space.Total, 10)
		poolinfo[infostring][spaceusedstring] = strconv.FormatUint(res.Space.Used, 10)
	} else {
		poolinfo[infostring][totalspacestring] = units.GetByteSizeStringIEC(int64(res.Space.Total), 2)
		poolinfo[infostring][spaceusedstring] = units.GetByteSizeStringIEC(int64(res.Space.Used), 2)
	}

	poolinfodata, err := yaml.Marshal(poolinfo)
	if err != nil {
		return err
	}

	poolusedbydata, err := yaml.Marshal(poolusedby)
	if err != nil {
		return err
	}

	fmt.Printf("%s", poolinfodata)
	fmt.Printf("%s", poolusedbydata)

	return nil
}

// List.
type cmdStorageList struct {
	global  *cmdGlobal
	storage *cmdStorage

	flagFormat string
}

func (c *cmdStorageList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available storage pools")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List available storage pools`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageList) run(cmd *cobra.Command, args []string) error {
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

	// Get the storage pools
	pools, err := resource.server.GetStoragePools()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, pool := range pools {
		usedby := strconv.Itoa(len(pool.UsedBy))
		details := []string{pool.Name, pool.Driver}
		if !resource.server.IsClustered() {
			details = append(details, pool.Config["source"])
		}

		details = append(details, pool.Description)
		details = append(details, usedby)
		details = append(details, strings.ToUpper(pool.Status))
		data = append(data, details)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DRIVER"),
	}

	if !resource.server.IsClustered() {
		header = append(header, i18n.G("SOURCE"))
	}

	header = append(header, i18n.G("DESCRIPTION"))
	header = append(header, i18n.G("USED BY"))
	header = append(header, i18n.G("STATE"))

	return cli.RenderTable(c.flagFormat, header, data, pools)
}

// Set.
type cmdStorageSet struct {
	global  *cmdGlobal
	storage *cmdStorage

	flagIsProperty bool
}

func (c *cmdStorageSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<pool> <key> <value>"))
	cmd.Short = i18n.G("Set storage pool configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set storage pool configuration keys

For backward compatibility, a single configuration key may still be set with:
    lxc storage set [<remote>:]<pool> <key> <value>`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Set the key as a storage property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageSet) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse remote
	remote := args[0]

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return errors.New(i18n.G("Missing pool name"))
	}

	client := resource.server
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	// Get the pool entry
	pool, etag, err := client.GetStoragePool(resource.name)
	if err != nil {
		return err
	}

	// Parse key/values
	keys, err := getConfig(args[1:]...)
	if err != nil {
		return err
	}

	writable := pool.Writable()
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
		if writable.Config == nil {
			writable.Config = make(map[string]string)
		}

		// Update the volume config keys.
		for k, v := range keys {
			writable.Config[k] = v
		}
	}

	err = client.UpdateStoragePool(resource.name, writable, etag)
	if err != nil {
		return err
	}

	return nil
}

// Show.
type cmdStorageShow struct {
	global  *cmdGlobal
	storage *cmdStorage

	flagResources bool
}

func (c *cmdStorageShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<pool>"))
	cmd.Short = i18n.G("Show storage pool configurations and resources")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show storage pool configurations and resources`))

	cmd.Flags().BoolVar(&c.flagResources, "resources", false, i18n.G("Show the resources available to the storage pool"))
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageShow) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
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
	client := resource.server

	if resource.name == "" {
		return errors.New(i18n.G("Missing pool name"))
	}

	// If a target member was specified, we return also member-specific config values.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	if c.flagResources {
		res, err := client.GetStoragePoolResources(resource.name)
		if err != nil {
			return err
		}

		data, err := yaml.Marshal(&res)
		if err != nil {
			return err
		}

		fmt.Printf("%s", data)

		return nil
	}

	pool, _, err := client.GetStoragePool(resource.name)
	if err != nil {
		return err
	}

	sort.Strings(pool.UsedBy)

	data, err := yaml.Marshal(&pool)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// Unset.
type cmdStorageUnset struct {
	global     *cmdGlobal
	storage    *cmdStorage
	storageSet *cmdStorageSet

	flagIsProperty bool
}

func (c *cmdStorageUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<pool> <key>"))
	cmd.Short = i18n.G("Unset storage pool configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset storage pool configuration keys`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVarP(&c.flagIsProperty, "property", "p", false, i18n.G("Unset the key as a storage property"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpStoragePools(toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolConfigs(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageUnset) run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	c.storageSet.flagIsProperty = c.flagIsProperty

	args = append(args, "")
	return c.storageSet.run(cmd, args)
}
