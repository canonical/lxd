package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
	"github.com/lxc/lxd/shared/units"
)

type cmdStorage struct {
	global *cmdGlobal

	flagTarget string
}

func (c *cmdStorage) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("storage")
	cmd.Short = i18n.G("Manage storage pools and volumes")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage storage pools and volumes`))

	// Create
	storageCreateCmd := cmdStorageCreate{global: c.global, storage: c}
	cmd.AddCommand(storageCreateCmd.Command())

	// Delete
	storageDeleteCmd := cmdStorageDelete{global: c.global, storage: c}
	cmd.AddCommand(storageDeleteCmd.Command())

	// Edit
	storageEditCmd := cmdStorageEdit{global: c.global, storage: c}
	cmd.AddCommand(storageEditCmd.Command())

	// Get
	storageGetCmd := cmdStorageGet{global: c.global, storage: c}
	cmd.AddCommand(storageGetCmd.Command())

	// Info
	storageInfoCmd := cmdStorageInfo{global: c.global, storage: c}
	cmd.AddCommand(storageInfoCmd.Command())

	// List
	storageListCmd := cmdStorageList{global: c.global, storage: c}
	cmd.AddCommand(storageListCmd.Command())

	// Set
	storageSetCmd := cmdStorageSet{global: c.global, storage: c}
	cmd.AddCommand(storageSetCmd.Command())

	// Show
	storageShowCmd := cmdStorageShow{global: c.global, storage: c}
	cmd.AddCommand(storageShowCmd.Command())

	// Unset
	storageUnsetCmd := cmdStorageUnset{global: c.global, storage: c, storageSet: &storageSetCmd}
	cmd.AddCommand(storageUnsetCmd.Command())

	// Volume
	storageVolumeCmd := cmdStorageVolume{global: c.global, storage: c}
	cmd.AddCommand(storageVolumeCmd.Command())

	return cmd
}

// Create
type cmdStorageCreate struct {
	global  *cmdGlobal
	storage *cmdStorage
}

func (c *cmdStorageCreate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("create", i18n.G("[<remote>:]<pool> <driver> [key=value...]"))
	cmd.Short = i18n.G("Create storage pools")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Create storage pools`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageCreate) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	client := resource.server

	// Create the new storage pool entry
	pool := api.StoragePoolsPost{}
	pool.Name = resource.name
	pool.Config = map[string]string{}
	pool.Driver = args[1]

	for i := 2; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return fmt.Errorf(i18n.G("Bad key=value pair: %s"), entry)
		}

		pool.Config[entry[0]] = entry[1]
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

// Delete
type cmdStorageDelete struct {
	global  *cmdGlobal
	storage *cmdStorage
}

func (c *cmdStorageDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<pool>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete storage pools")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete storage pools`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageDelete) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing pool name"))
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

// Edit
type cmdStorageEdit struct {
	global  *cmdGlobal
	storage *cmdStorage
}

func (c *cmdStorageEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<pool>"))
	cmd.Short = i18n.G("Edit storage pool configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit storage pool configurations as YAML`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc storage edit [<remote>:]<pool> < pool.yaml
    Update a storage pool using the content of pool.yaml.`))

	cmd.RunE = c.Run

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

func (c *cmdStorageEdit) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := ioutil.ReadAll(os.Stdin)
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

// Get
type cmdStorageGet struct {
	global  *cmdGlobal
	storage *cmdStorage
}

func (c *cmdStorageGet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("get", i18n.G("[<remote>:]<pool> <key>"))
	cmd.Short = i18n.G("Get values for storage pool configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get values for storage pool configuration keys`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageGet) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing pool name"))
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

	for k, v := range resp.Config {
		if k == args[1] {
			fmt.Printf("%s\n", v)
		}
	}

	return nil
}

// Info
type cmdStorageInfo struct {
	global  *cmdGlobal
	storage *cmdStorage

	flagBytes bool
}

func (c *cmdStorageInfo) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", i18n.G("[<remote>:]<pool>"))
	cmd.Short = i18n.G("Show useful information about storage pools")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show useful information about storage pools`))

	cmd.Flags().BoolVar(&c.flagBytes, "bytes", false, i18n.G("Show the used and free space in bytes"))
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageInfo) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing pool name"))
	}

	// Targeting
	if c.storage.flagTarget != "" {
		if !resource.server.IsClustered() {
			return fmt.Errorf(i18n.G("To use --target, the destination remote must be a cluster"))
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
	poolusedby[usedbystring] = map[string][]string{}

	/* Build up the usedby map
	/1.0/{instances,images,profiles}/storagepoolname
	remove the /1.0/ and build the map based on the resources name as key
	and resources details as value */
	for _, v := range pool.UsedBy {
		bytype := string(strings.Split(v[5:], "/")[0])
		bywhat := string(strings.Split(v[5:], "/")[1])

		poolusedby[usedbystring][bytype] = append(poolusedby[usedbystring][bytype], bywhat)
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
		poolinfo[infostring][totalspacestring] = units.GetByteSizeString(int64(res.Space.Total), 2)
		poolinfo[infostring][spaceusedstring] = units.GetByteSizeString(int64(res.Space.Used), 2)
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

// List
type cmdStorageList struct {
	global  *cmdGlobal
	storage *cmdStorage

	flagFormat string
}

func (c *cmdStorageList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List available storage pools")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List available storage pools`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml)")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageList) Run(cmd *cobra.Command, args []string) error {
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
		if resource.server.IsClustered() {
			details = append(details, strings.ToUpper(pool.Status))
		}

		data = append(data, details)
	}
	sort.Sort(byName(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("DRIVER"),
	}
	if !resource.server.IsClustered() {
		header = append(header, i18n.G("SOURCE"))
	}

	header = append(header, i18n.G("DESCRIPTION"))
	header = append(header, i18n.G("USED BY"))
	if resource.server.IsClustered() {
		header = append(header, i18n.G("STATE"))
	}

	return utils.RenderTable(c.flagFormat, header, data, pools)
}

// Set
type cmdStorageSet struct {
	global  *cmdGlobal
	storage *cmdStorage
}

func (c *cmdStorageSet) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("set", i18n.G("[<remote>:]<pool> <key> <value>"))
	cmd.Short = i18n.G("Set storage pool configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Set storage pool configuration keys

For backward compatibility, a single configuration key may still be set with:
    lxc storage set [<remote>:]<pool> <key> <value>`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageSet) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing pool name"))
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

	// Update the pool
	for k, v := range keys {
		pool.Config[k] = v
	}

	err = client.UpdateStoragePool(resource.name, pool.Writable(), etag)
	if err != nil {
		return err
	}

	return nil
}

// Show
type cmdStorageShow struct {
	global  *cmdGlobal
	storage *cmdStorage

	flagResources bool
}

func (c *cmdStorageShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<pool>"))
	cmd.Short = i18n.G("Show storage pool configurations and resources")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show storage pool configurations and resources`))

	cmd.Flags().BoolVar(&c.flagResources, "resources", false, i18n.G("Show the resources available to the storage pool"))
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageShow) Run(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf(i18n.G("Missing pool name"))
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

// Unset
type cmdStorageUnset struct {
	global     *cmdGlobal
	storage    *cmdStorage
	storageSet *cmdStorageSet
}

func (c *cmdStorageUnset) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("unset", i18n.G("[<remote>:]<pool> <key>"))
	cmd.Short = i18n.G("Unset storage pool configuration keys")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Unset storage pool configuration keys`))

	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

func (c *cmdStorageUnset) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	args = append(args, "")
	return c.storageSet.Run(cmd, args)
}
