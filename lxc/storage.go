package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type storageCmd struct {
	resources bool
}

func (c *storageCmd) showByDefault() bool {
	return true
}

func (c *storageCmd) storagePoolEditHelp() string {
	return i18n.G(
		`### This is a yaml representation of a storage pool.
### Any line starting with a '# will be ignored.
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

func (c *storageCmd) storagePoolVolumeEditHelp() string {
	return i18n.G(
		`### This is a yaml representation of a storage volume.
### Any line starting with a '# will be ignored.
###
### A storage volume consists of a set of configuration items.
###
### name: vol1
### type: custom
### used_by: []
### config:
###   size: "61203283968"`)
}

func (c *storageCmd) usage() string {
	return i18n.G(
		`Usage: lxc storage <subcommand> [options]

Manage storage pools and volumes.

*Storage pools*
lxc storage list [<remote>:]
    List available storage pools.

lxc storage show [<remote>:]<pool> [--resources]
    Show details of a storage pool.

lxc storage create [<remote>:]<pool> <driver> [key=value]...
    Create a storage pool.

lxc storage get [<remote>:]<pool> <key>
    Get storage pool configuration.

lxc storage set [<remote>:]<pool> <key> <value>
    Set storage pool configuration.

lxc storage unset [<remote>:]<pool> <key>
    Unset storage pool configuration.

lxc storage delete [<remote>:]<pool>
    Delete a storage pool.

lxc storage edit [<remote>:]<pool>
    Edit storage pool, either by launching external editor or reading STDIN.

*Storage volumes*
lxc storage volume list [<remote>:]<pool>
    List available storage volumes on a storage pool.

lxc storage volume show [<remote>:]<pool> <volume>
    Show details of a storage volume on a storage pool.

lxc storage volume create [<remote>:]<pool> <volume> [key=value]...
    Create a storage volume on a storage pool.

lxc storage volume rename [<remote>:]<pool> <old name> <new name>
    Rename a storage volume on a storage pool.

lxc storage volume get [<remote>:]<pool> <volume> <key>
    Get storage volume configuration on a storage pool.

lxc storage volume set [<remote>:]<pool> <volume> <key> <value>
    Set storage volume configuration on a storage pool.

lxc storage volume unset [<remote>:]<pool> <volume> <key>
    Unset storage volume configuration on a storage pool.

lxc storage volume delete [<remote>:]<pool> <volume>
    Delete a storage volume on a storage pool.

lxc storage volume edit [<remote>:]<pool> <volume>
    Edit storage pool, either by launching external editor or reading STDIN.

lxc storage volume attach [<remote>:]<pool> <volume> <container> [device name] <path>
    Attach a storage volume to the specified container.

lxc storage volume attach-profile [<remote:>]<pool> <volume> <profile> [device name] <path>
    Attach a storage volume to the specified profile.

lxc storage volume detach [<remote>:]<pool> <volume> <container> [device name]
    Detach a storage volume from the specified container.

lxc storage volume detach-profile [<remote:>]<pool> <volume> <profile> [device name]
    Detach a storage volume from the specified profile.

Unless specified through a prefix, all volume operations affect "custom" (user created) volumes.

*Examples*
cat pool.yaml | lxc storage edit [<remote>:]<pool>
    Update a storage pool using the content of pool.yaml.

cat pool.yaml | lxc storage volume edit [<remote>:]<pool> <volume>
    Update a storage volume using the content of pool.yaml.

lxc storage volume show default data
    Will show the properties of a custom volume called "data" in the "default" pool.

lxc storage volume show default container/data
    Will show the properties of the filesystem for a container called "data" in the "default" pool.`)
}

func (c *storageCmd) flags() {
	gnuflag.BoolVar(&c.resources, "resources", false, i18n.G("Show the resources available to the storage pool"))
}

func (c *storageCmd) run(conf *config.Config, args []string) error {
	if len(args) < 1 {
		return errUsage
	}

	if args[0] == "list" {
		return c.doStoragePoolsList(conf, args)
	}

	if len(args) < 2 {
		return errArgs
	}

	if args[0] == "volume" {
		if len(args) < 3 {
			return errArgs
		}

		remote, sub, err := conf.ParseRemote(args[2])
		if err != nil {
			return err
		}

		client, err := conf.GetContainerServer(remote)
		if err != nil {
			return err
		}

		switch args[1] {
		case "attach":
			if len(args) < 5 {
				return errArgs
			}
			pool := sub
			volume := args[3]
			return c.doStoragePoolVolumeAttach(client, pool, volume, args[4:])
		case "attach-profile":
			if len(args) < 5 {
				return errArgs
			}
			pool := sub
			volume := args[3]
			return c.doStoragePoolVolumeAttachProfile(client, pool, volume, args[4:])
		case "create":
			if len(args) < 4 {
				return errArgs
			}
			pool := sub
			volume := args[3]
			return c.doStoragePoolVolumeCreate(client, pool, volume, args[4:])
		case "delete":
			if len(args) != 4 {
				return errArgs
			}
			pool := sub
			volume := args[3]
			return c.doStoragePoolVolumeDelete(client, pool, volume)
		case "detach":
			if len(args) < 4 {
				return errArgs
			}
			pool := sub
			volume := args[3]
			return c.doStoragePoolVolumeDetach(client, pool, volume, args[4:])
		case "detach-profile":
			if len(args) < 5 {
				return errArgs
			}
			pool := sub
			volume := args[3]
			return c.doStoragePoolVolumeDetachProfile(client, pool, volume, args[4:])
		case "edit":
			if len(args) != 4 {
				return errArgs
			}
			pool := sub
			volume := args[3]
			return c.doStoragePoolVolumeEdit(client, pool, volume)
		case "get":
			if len(args) < 4 {
				return errArgs
			}
			pool := sub
			volume := args[3]
			return c.doStoragePoolVolumeGet(client, pool, volume, args[3:])
		case "list":
			if len(args) != 3 {
				return errArgs
			}
			pool := sub
			return c.doStoragePoolVolumesList(conf, remote, pool, args)
		case "rename":
			if len(args) != 5 {
				return errArgs
			}
			pool := sub
			volume := args[3]
			return c.doStoragePoolVolumeRename(client, pool, volume, args)
		case "set":
			if len(args) < 4 {
				return errArgs
			}
			pool := sub
			volume := args[3]
			return c.doStoragePoolVolumeSet(client, pool, volume, args[3:])
		case "unset":
			if len(args) < 4 {
				return errArgs
			}
			pool := sub
			volume := args[3]
			return c.doStoragePoolVolumeSet(client, pool, volume, args[3:])
		case "show":
			if len(args) != 4 {
				return errArgs
			}
			pool := sub
			volume := args[3]
			return c.doStoragePoolVolumeShow(client, pool, volume)
		default:
			return errArgs
		}
	} else {
		remote, sub, err := conf.ParseRemote(args[1])
		if err != nil {
			return err
		}

		client, err := conf.GetContainerServer(remote)
		if err != nil {
			return err
		}

		pool := sub
		switch args[0] {
		case "create":
			if len(args) < 3 {
				return errArgs
			}
			driver := args[2]
			return c.doStoragePoolCreate(client, pool, driver, args[3:])
		case "delete":
			return c.doStoragePoolDelete(client, pool)
		case "edit":
			return c.doStoragePoolEdit(client, pool)
		case "get":
			if len(args) < 2 {
				return errArgs
			}
			return c.doStoragePoolGet(client, pool, args[2:])
		case "set":
			if len(args) < 2 {
				return errArgs
			}
			return c.doStoragePoolSet(client, pool, args[2:])
		case "unset":
			if len(args) < 2 {
				return errArgs
			}
			return c.doStoragePoolSet(client, pool, args[2:])
		case "show":
			if len(args) < 2 {
				return errArgs
			}
			return c.doStoragePoolShow(client, pool)
		default:
			return errArgs
		}
	}
}

func (c *storageCmd) parseVolume(name string) (string, string) {
	defaultType := "custom"

	fields := strings.SplitN(name, "/", 2)
	if len(fields) == 1 {
		return fields[0], defaultType
	}

	return fields[1], fields[0]
}

func (c *storageCmd) doStoragePoolVolumeAttach(client lxd.ContainerServer, pool string, volume string, args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return errArgs
	}

	devPath := ""
	devName := ""
	if len(args) == 2 {
		// Only the path has been given to us.
		devPath = args[1]
		devName = volume
	} else if len(args) == 3 {
		// Path and device name have been given to us.
		devName = args[1]
		devPath = args[2]
	}

	volName, volType := c.parseVolume(volume)
	if volType != "custom" {
		return fmt.Errorf(i18n.G("Only \"custom\" volumes can be attached to containers."))
	}

	// Check if the requested storage volume actually exists
	vol, _, err := client.GetStoragePoolVolume(pool, volType, volName)
	if err != nil {
		return err
	}

	// Prepare the container's device entry
	device := map[string]string{
		"type":   "disk",
		"pool":   pool,
		"path":   devPath,
		"source": vol.Name,
	}

	// Add the device to the container
	err = containerDeviceAdd(client, args[0], devName, device)
	if err != nil {
		return err
	}

	return nil
}

func (c *storageCmd) doStoragePoolVolumeDetach(client lxd.ContainerServer, pool string, volume string, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errArgs
	}

	devName := ""
	if len(args) == 2 {
		devName = args[1]
	}

	// Get the container entry
	container, etag, err := client.GetContainer(args[0])
	if err != nil {
		return err
	}

	// Find the device
	if devName == "" {
		for n, d := range container.Devices {
			if d["type"] == "disk" && d["pool"] == pool && d["source"] == volume {
				if devName != "" {
					return fmt.Errorf(i18n.G("More than one device matches, specify the device name."))
				}

				devName = n
			}
		}
	}

	if devName == "" {
		return fmt.Errorf(i18n.G("No device found for this storage volume."))
	}

	_, ok := container.Devices[devName]
	if !ok {
		return fmt.Errorf(i18n.G("The specified device doesn't exist"))
	}

	// Remove the device
	delete(container.Devices, devName)
	op, err := client.UpdateContainer(args[0], container.Writable(), etag)
	if err != nil {
		return err
	}

	return op.Wait()
}

func (c *storageCmd) doStoragePoolVolumeAttachProfile(client lxd.ContainerServer, pool string, volume string, args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return errArgs
	}

	devPath := ""
	devName := ""
	if len(args) == 2 {
		// Only the path has been given to us.
		devPath = args[1]
		devName = volume
	} else if len(args) == 3 {
		// Path and device name have been given to us.
		devName = args[1]
		devPath = args[2]
	}

	volName, volType := c.parseVolume(volume)
	if volType != "custom" {
		return fmt.Errorf(i18n.G("Only \"custom\" volumes can be attached to containers."))
	}

	// Check if the requested storage volume actually exists
	vol, _, err := client.GetStoragePoolVolume(pool, volType, volName)
	if err != nil {
		return err
	}

	// Prepare the container's device entry
	device := map[string]string{
		"type":   "disk",
		"pool":   pool,
		"path":   devPath,
		"source": vol.Name,
	}

	// Add the device to the container
	err = profileDeviceAdd(client, args[0], devName, device)
	if err != nil {
		return err
	}

	return nil
}

func (c *storageCmd) doStoragePoolCreate(client lxd.ContainerServer, name string, driver string, args []string) error {
	// Create the new storage pool entry
	pool := api.StoragePoolsPost{}
	pool.Name = name
	pool.Config = map[string]string{}
	pool.Driver = driver

	for i := 0; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return errArgs
		}

		pool.Config[entry[0]] = entry[1]
	}

	// Create the pool
	err := client.CreateStoragePool(pool)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Storage pool %s created")+"\n", name)

	return nil
}

func (c *storageCmd) doStoragePoolVolumeDetachProfile(client lxd.ContainerServer, pool string, volume string, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errArgs
	}

	devName := ""
	if len(args) > 1 {
		devName = args[1]
	}

	// Get the profile entry
	profile, etag, err := client.GetProfile(args[0])
	if err != nil {
		return err
	}

	// Find the device
	if devName == "" {
		for n, d := range profile.Devices {
			if d["type"] == "disk" && d["pool"] == pool && d["source"] == volume {
				if devName != "" {
					return fmt.Errorf(i18n.G("More than one device matches, specify the device name."))
				}

				devName = n
			}
		}
	}

	if devName == "" {
		return fmt.Errorf(i18n.G("No device found for this storage volume."))
	}

	_, ok := profile.Devices[devName]
	if !ok {
		return fmt.Errorf(i18n.G("The specified device doesn't exist"))
	}

	// Remove the device
	delete(profile.Devices, devName)
	err = client.UpdateProfile(args[0], profile.Writable(), etag)
	if err != nil {
		return err
	}

	return nil
}

func (c *storageCmd) doStoragePoolDelete(client lxd.ContainerServer, name string) error {
	err := client.DeleteStoragePool(name)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Storage pool %s deleted")+"\n", name)

	return nil
}

func (c *storageCmd) doStoragePoolEdit(client lxd.ContainerServer, name string) error {
	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.StoragePoolPut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return client.UpdateStoragePool(name, newdata, "")
	}

	// Extract the current value
	pool, etag, err := client.GetStoragePool(name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&pool)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(c.storagePoolEditHelp()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.StoragePoolPut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = client.UpdateStoragePool(name, newdata, etag)
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

func (c *storageCmd) doStoragePoolGet(client lxd.ContainerServer, name string, args []string) error {
	// we shifted @args so so it should read "<key>"
	if len(args) != 1 {
		return errArgs
	}

	resp, _, err := client.GetStoragePool(name)
	if err != nil {
		return err
	}

	for k, v := range resp.Config {
		if k == args[0] {
			fmt.Printf("%s\n", v)
		}
	}
	return nil
}

func (c *storageCmd) doStoragePoolsList(conf *config.Config, args []string) error {
	var remote string
	if len(args) > 1 {
		var name string
		var err error
		remote, name, err = conf.ParseRemote(args[1])
		if err != nil {
			return err
		}

		if name != "" {
			return fmt.Errorf(i18n.G("Cannot provide container name to list"))
		}
	} else {
		remote = conf.DefaultRemote
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	pools, err := client.GetStoragePools()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, pool := range pools {
		usedby := strconv.Itoa(len(pool.UsedBy))

		data = append(data, []string{pool.Name, pool.Description, pool.Driver, pool.Config["source"], usedby})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("DRIVER"),
		i18n.G("SOURCE"),
		i18n.G("USED BY")})
	sort.Sort(byName(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (c *storageCmd) doStoragePoolSet(client lxd.ContainerServer, name string, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	// Get the pool entry
	pool, etag, err := client.GetStoragePool(name)
	if err != nil {
		return err
	}

	// Read the value
	var value string
	if len(args) < 2 {
		value = ""
	} else {
		value = args[1]
	}

	if !termios.IsTerminal(int(syscall.Stdin)) && value == "-" {
		buf, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("Can't read from stdin: %s", err)
		}
		value = string(buf[:])
	}

	// Update the pool
	pool.Config[args[0]] = value

	err = client.UpdateStoragePool(name, pool.Writable(), etag)
	if err != nil {
		return err
	}

	return nil
}

func (c *storageCmd) doStoragePoolShow(client lxd.ContainerServer, name string) error {
	if name == "" {
		return errArgs
	}

	if c.resources {
		res, err := client.GetStoragePoolResources(name)
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

	pool, _, err := client.GetStoragePool(name)
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

func (c *storageCmd) doStoragePoolVolumesList(conf *config.Config, remote string, pool string, args []string) error {
	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	volumes, err := client.GetStoragePoolVolumes(pool)
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, volume := range volumes {
		usedby := strconv.Itoa(len(volume.UsedBy))
		data = append(data, []string{volume.Type, volume.Name, volume.Description, usedby})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("TYPE"),
		i18n.G("NAME"),
		i18n.G("DESCRIPTION"),
		i18n.G("USED BY")})
	sort.Sort(byNameAndType(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (c *storageCmd) doStoragePoolVolumeCreate(client lxd.ContainerServer, pool string, volume string, args []string) error {
	// Parse the input
	volName, volType := c.parseVolume(volume)

	// Create the storage volume entry
	vol := api.StorageVolumesPost{}
	vol.Name = volName
	vol.Type = volType
	vol.Config = map[string]string{}

	for i := 0; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return errArgs
		}

		vol.Config[entry[0]] = entry[1]
	}

	err := client.CreateStoragePoolVolume(pool, vol)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Storage volume %s created")+"\n", volume)

	return nil
}

func (c *storageCmd) doStoragePoolVolumeDelete(client lxd.ContainerServer, pool string, volume string) error {
	// Parse the input
	volName, volType := c.parseVolume(volume)

	// Delete the volume
	err := client.DeleteStoragePoolVolume(pool, volType, volName)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Storage volume %s deleted")+"\n", volume)

	return nil
}

func (c *storageCmd) doStoragePoolVolumeGet(client lxd.ContainerServer, pool string, volume string, args []string) error {
	if len(args) != 2 {
		return errArgs
	}

	// Parse input
	volName, volType := c.parseVolume(volume)

	// Get the storage volume entry
	resp, _, err := client.GetStoragePoolVolume(pool, volType, volName)
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

func (c *storageCmd) doStoragePoolVolumeSet(client lxd.ContainerServer, pool string, volume string, args []string) error {
	if len(args) < 2 {
		return errArgs
	}

	// Parse the input
	volName, volType := c.parseVolume(volume)

	// Get the storage volume entry
	vol, etag, err := client.GetStoragePoolVolume(pool, volType, volName)
	if err != nil {
		return err
	}

	// Get the value
	key := args[1]
	var value string
	if len(args) < 3 {
		value = ""
	} else {
		value = args[2]
	}

	if !termios.IsTerminal(int(syscall.Stdin)) && value == "-" {
		buf, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("Can't read from stdin: %s", err)
		}
		value = string(buf[:])
	}

	// Update the volume
	vol.Config[key] = value
	err = client.UpdateStoragePoolVolume(pool, vol.Type, vol.Name, vol.Writable(), etag)
	if err != nil {
		return err
	}

	return nil
}

func (c *storageCmd) doStoragePoolVolumeShow(client lxd.ContainerServer, pool string, volume string) error {
	// Parse the input
	volName, volType := c.parseVolume(volume)

	// Get the storage volume entry
	vol, _, err := client.GetStoragePoolVolume(pool, volType, volName)
	if err != nil {
		return err
	}

	sort.Strings(vol.UsedBy)

	data, err := yaml.Marshal(&vol)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

func (c *storageCmd) doStoragePoolVolumeEdit(client lxd.ContainerServer, pool string, volume string) error {
	// Parse the input
	volName, volType := c.parseVolume(volume)

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.StorageVolumePut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return client.UpdateStoragePoolVolume(pool, volType, volName, newdata, "")
	}

	// Extract the current value
	vol, etag, err := client.GetStoragePoolVolume(pool, volType, volName)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&vol)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := shared.TextEditor("", []byte(c.storagePoolVolumeEditHelp()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.StorageVolume{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = client.UpdateStoragePoolVolume(pool, vol.Type, vol.Name, newdata.Writable(), etag)
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

func (c *storageCmd) doStoragePoolVolumeRename(client lxd.ContainerServer, pool string, volume string, args []string) error {
	// Parse the input
	volName, volType := c.parseVolume(volume)

	// Create the storage volume entry
	vol := api.StorageVolumePost{}
	vol.Name = args[4]

	err := client.RenameStoragePoolVolume(pool, volType, volName, vol)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G(`Renamed storage volume from "%s" to "%s"`)+"\n", volName, vol.Name)

	return nil
}
