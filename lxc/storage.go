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

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/termios"
)

type byNameAndType [][]string

func (a byNameAndType) Len() int {
	return len(a)
}

func (a byNameAndType) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a byNameAndType) Less(i, j int) bool {
	if a[i][0] != a[j][0] {
		return a[i][0] < a[j][0]
	}

	if a[i][1] == "" {
		return false
	}

	if a[j][1] == "" {
		return true
	}

	return a[i][1] < a[j][1]
}

type storageCmd struct {
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
		`Manage storage.

lxc storage list [<remote>:]                                    List available storage pools.
lxc storage show [<remote>:]<pool>                              Show details of a storage pool.
lxc storage create [<remote>:]<pool> <driver> [key=value]...    Create a storage pool.
lxc storage get [<remote>:]<pool> <key>                         Get storage pool configuration.
lxc storage set [<remote>:]<pool> <key> <value>                 Set storage pool configuration.
lxc storage unset [<remote>:]<pool> <key>                       Unset storage pool configuration.
lxc storage delete [<remote>:]<pool>                            Delete a storage pool.
lxc storage edit [<remote>:]<pool>
    Edit storage pool, either by launching external editor or reading STDIN.
    Example: lxc storage edit [<remote>:]<pool> # launch editor
             cat pool.yaml | lxc storage edit [<remote>:]<pool> # read from pool.yaml

lxc storage volume list [<remote>:]<pool>                              List available storage volumes on a storage pool.
lxc storage volume show [<remote>:]<pool> <volume>                     Show details of a storage volume on a storage pool.
lxc storage volume create [<remote>:]<pool> <volume> [key=value]...    Create a storage volume on a storage pool.
lxc storage volume get [<remote>:]<pool> <volume> <key>                Get storage volume configuration on a storage pool.
lxc storage volume set [<remote>:]<pool> <volume> <key> <value>        Set storage volume configuration on a storage pool.
lxc storage volume unset [<remote>:]<pool> <volume> <key>              Unset storage volume configuration on a storage pool.
lxc storage volume delete [<remote>:]<pool> <volume>                   Delete a storage volume on a storage pool.
lxc storage volume edit [<remote>:]<pool> <volume>
    Edit storage pool, either by launching external editor or reading STDIN.
    Example: lxc storage volume edit [<remote>:]<pool> <volume> # launch editor
             cat pool.yaml | lxc storage volume edit [<remote>:]<pool> <volume> # read from pool.yaml

lxc storage volume attach [<remote>:]<pool> <volume> <container> [device name] <path>
lxc storage volume attach-profile [<remote:>]<pool> <volume> <profile> [device name] <path>

lxc storage volume detach [<remote>:]<pool> <volume> <container> [device name]
lxc storage volume detach-profile [<remote:>]<pool> <volume> <profile> [device name]


Unless specified through a prefix, all volume operations affect "custom" (user created) volumes.

Examples:
To show the properties of a custom volume called "data" in the "default" pool:
    lxc storage volume show default data

To show the properties of the filesystem for a container called "data" in the "default" pool:
    lxc storage volume show default container/data
`)
}

func (c *storageCmd) flags() {}

func (c *storageCmd) run(config *lxd.Config, args []string) error {
	if len(args) < 1 {
		return errArgs
	}

	if args[0] == "list" {
		return c.doStoragePoolsList(config, args)
	}

	if len(args) < 2 {
		return errArgs
	}

	remote, sub := config.ParseRemoteAndContainer(args[1])
	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	if args[0] == "volume" {
		switch args[1] {
		case "attach":
			if len(args) < 5 {
				return errArgs
			}
			pool := args[2]
			volume := args[3]
			return c.doStoragePoolVolumeAttach(client, pool, volume, args[4:])
		case "attach-profile":
			if len(args) < 5 {
				return errArgs
			}
			pool := args[2]
			volume := args[3]
			return c.doStoragePoolVolumeAttachProfile(client, pool, volume, args[4:])
		case "create":
			if len(args) < 4 {
				return errArgs
			}
			pool := args[2]
			volume := args[3]
			return c.doStoragePoolVolumeCreate(client, pool, volume, args[4:])
		case "delete":
			if len(args) != 4 {
				return errArgs
			}
			pool := args[2]
			volume := args[3]
			return c.doStoragePoolVolumeDelete(client, pool, volume)
		case "detach":
			if len(args) < 4 {
				return errArgs
			}
			pool := args[2]
			volume := args[3]
			return c.doStoragePoolVolumeDetach(client, pool, volume, args[4:])
		case "detach-profile":
			if len(args) < 5 {
				return errArgs
			}
			pool := args[2]
			volume := args[3]
			return c.doStoragePoolVolumeDetachProfile(client, pool, volume, args[4:])
		case "edit":
			if len(args) != 4 {
				return errArgs
			}
			pool := args[2]
			volume := args[3]
			return c.doStoragePoolVolumeEdit(client, pool, volume)
		case "get":
			if len(args) < 4 {
				return errArgs
			}
			pool := args[2]
			volume := args[3]
			return c.doStoragePoolVolumeGet(client, pool, volume, args[3:])
		case "list":
			if len(args) != 3 {
				return errArgs
			}
			pool := args[2]
			return c.doStoragePoolVolumesList(config, remote, pool, args)
		case "set":
			if len(args) < 4 {
				return errArgs
			}
			pool := args[2]
			volume := args[3]
			return c.doStoragePoolVolumeSet(client, pool, volume, args[3:])
		case "unset":
			if len(args) < 4 {
				return errArgs
			}
			pool := args[2]
			volume := args[3]
			return c.doStoragePoolVolumeSet(client, pool, volume, args[3:])
		case "show":
			if len(args) != 4 {
				return errArgs
			}
			pool := args[2]
			volume := args[3]
			return c.doStoragePoolVolumeShow(client, pool, volume)
		default:
			return errArgs
		}
	} else {
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

func (c *storageCmd) doStoragePoolVolumeAttach(client *lxd.Client, pool string, volume string, args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return errArgs
	}

	container := args[0]
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

	// Check if the requested storage volume actually
	// exists on the requested storage pool.
	vol, err := client.StoragePoolVolumeTypeGet(pool, volName, volType)
	if err != nil {
		return err
	}

	props := []string{fmt.Sprintf("pool=%s", pool), fmt.Sprintf("path=%s", devPath), fmt.Sprintf("source=%s", vol.Name)}
	resp, err := client.ContainerDeviceAdd(container, devName, "disk", props)
	if err != nil {
		return err
	}

	return client.WaitForSuccess(resp.Operation)
}

func (c *storageCmd) doStoragePoolVolumeDetach(client *lxd.Client, pool string, volume string, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errArgs
	}

	containerName := args[0]
	devName := ""
	if len(args) == 2 {
		devName = args[1]
	}

	container, err := client.ContainerInfo(containerName)
	if err != nil {
		return err
	}

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

	resp, err := client.ContainerDeviceDelete(containerName, devName)
	if err != nil {
		return err
	}

	return client.WaitForSuccess(resp.Operation)
}

func (c *storageCmd) doStoragePoolVolumeAttachProfile(client *lxd.Client, pool string, volume string, args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return errArgs
	}

	profile := args[0]
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

	// Check if the requested storage volume actually
	// exists on the requested storage pool.
	vol, err := client.StoragePoolVolumeTypeGet(pool, volName, volType)
	if err != nil {
		return err
	}

	props := []string{fmt.Sprintf("pool=%s", pool), fmt.Sprintf("path=%s", devPath), fmt.Sprintf("source=%s", vol.Name)}

	_, err = client.ProfileDeviceAdd(profile, devName, "disk", props)
	return err
}

func (c *storageCmd) doStoragePoolCreate(client *lxd.Client, name string, driver string, args []string) error {
	config := map[string]string{}

	for i := 0; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return errArgs
		}
		config[entry[0]] = entry[1]
	}

	err := client.StoragePoolCreate(name, driver, config)
	if err == nil {
		fmt.Printf(i18n.G("Storage pool %s created")+"\n", name)
	}

	return err
}

func (c *storageCmd) doStoragePoolVolumeDetachProfile(client *lxd.Client, pool string, volume string, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errArgs
	}

	profileName := args[0]
	devName := ""
	if len(args) > 1 {
		devName = args[1]
	}

	profile, err := client.ProfileConfig(profileName)
	if err != nil {
		return err
	}

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

	_, err = client.ProfileDeviceDelete(profileName, devName)
	return err
}

func (c *storageCmd) doStoragePoolDelete(client *lxd.Client, name string) error {
	err := client.StoragePoolDelete(name)
	if err == nil {
		fmt.Printf(i18n.G("Storage pool %s deleted")+"\n", name)
	}

	return err
}

func (c *storageCmd) doStoragePoolEdit(client *lxd.Client, name string) error {
	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.StoragePool{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}
		return client.StoragePoolPut(name, newdata)
	}

	// Extract the current value
	pool, err := client.StoragePoolGet(name)
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
		newdata := api.StoragePool{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = client.StoragePoolPut(name, newdata)
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

func (c *storageCmd) doStoragePoolGet(client *lxd.Client, name string, args []string) error {
	// we shifted @args so so it should read "<key>"
	if len(args) != 1 {
		return errArgs
	}

	resp, err := client.StoragePoolGet(name)
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

func (c *storageCmd) doStoragePoolsList(config *lxd.Config, args []string) error {
	var remote string
	if len(args) > 1 {
		var name string
		remote, name = config.ParseRemoteAndContainer(args[1])
		if name != "" {
			return fmt.Errorf(i18n.G("Cannot provide container name to list"))
		}
	} else {
		remote = config.DefaultRemote
	}

	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	pools, err := client.ListStoragePools()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, pool := range pools {
		usedby := strconv.Itoa(len(pool.UsedBy))

		data = append(data, []string{pool.Name, pool.Driver, pool.Config["source"], usedby})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("NAME"),
		i18n.G("DRIVER"),
		i18n.G("SOURCE"),
		i18n.G("USED BY")})
	sort.Sort(byName(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (c *storageCmd) doStoragePoolSet(client *lxd.Client, name string, args []string) error {
	// we shifted @args so so it should read "<key> [<value>]"
	if len(args) < 1 {
		return errArgs
	}

	pool, err := client.StoragePoolGet(name)
	if err != nil {
		return err
	}

	key := args[0]
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

	pool.Config[key] = value

	return client.StoragePoolPut(name, pool)
}

func (c *storageCmd) doStoragePoolShow(client *lxd.Client, name string) error {
	pool, err := client.StoragePoolGet(name)
	if err != nil {
		return err
	}

	sort.Strings(pool.UsedBy)

	data, err := yaml.Marshal(&pool)
	fmt.Printf("%s", data)

	return nil
}

func (c *storageCmd) doStoragePoolVolumesList(config *lxd.Config, remote string, pool string, args []string) error {
	client, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	volumes, err := client.StoragePoolVolumesList(pool)
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, volume := range volumes {
		usedby := strconv.Itoa(len(volume.UsedBy))
		data = append(data, []string{volume.Type, volume.Name, usedby})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("TYPE"),
		i18n.G("NAME"),
		i18n.G("USED BY")})
	sort.Sort(byNameAndType(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}

func (c *storageCmd) doStoragePoolVolumeCreate(client *lxd.Client, pool string, volume string, args []string) error {
	config := map[string]string{}

	for i := 0; i < len(args); i++ {
		entry := strings.SplitN(args[i], "=", 2)
		if len(entry) < 2 {
			return errArgs
		}
		config[entry[0]] = entry[1]
	}

	volName, volType := c.parseVolume(volume)
	err := client.StoragePoolVolumeTypeCreate(pool, volName, volType, config)
	if err == nil {
		fmt.Printf(i18n.G("Storage volume %s created")+"\n", volume)
	}

	return err
}

func (c *storageCmd) doStoragePoolVolumeDelete(client *lxd.Client, pool string, volume string) error {
	volName, volType := c.parseVolume(volume)
	err := client.StoragePoolVolumeTypeDelete(pool, volName, volType)
	if err == nil {
		fmt.Printf(i18n.G("Storage volume %s deleted")+"\n", volume)
	}

	return err
}

func (c *storageCmd) doStoragePoolVolumeGet(client *lxd.Client, pool string, volume string, args []string) error {
	// we shifted @args so so it should read "<key>"
	if len(args) != 2 {
		return errArgs
	}

	volName, volType := c.parseVolume(volume)
	resp, err := client.StoragePoolVolumeTypeGet(pool, volName, volType)
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

func (c *storageCmd) doStoragePoolVolumeSet(client *lxd.Client, pool string, volume string, args []string) error {
	// we shifted @args so so it should read "<key> [<value>]"
	if len(args) < 2 {
		return errArgs
	}

	volName, volType := c.parseVolume(volume)
	volumeConfig, err := client.StoragePoolVolumeTypeGet(pool, volName, volType)
	if err != nil {
		return err
	}

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

	volumeConfig.Config[key] = value

	return client.StoragePoolVolumeTypePut(pool, volName, volType, volumeConfig)
}

func (c *storageCmd) doStoragePoolVolumeShow(client *lxd.Client, pool string, volume string) error {
	volName, volType := c.parseVolume(volume)
	volumeStruct, err := client.StoragePoolVolumeTypeGet(pool, volName, volType)
	if err != nil {
		return err
	}

	sort.Strings(volumeStruct.UsedBy)

	data, err := yaml.Marshal(&volumeStruct)
	fmt.Printf("%s", data)

	return nil
}

func (c *storageCmd) doStoragePoolVolumeEdit(client *lxd.Client, pool string, volume string) error {
	volName, volType := c.parseVolume(volume)

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(int(syscall.Stdin)) {
		contents, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.StorageVolume{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return client.StoragePoolVolumeTypePut(pool, volName, volType, newdata)
	}

	// Extract the current value
	vol, err := client.StoragePoolVolumeTypeGet(pool, volName, volType)
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
			err = client.StoragePoolVolumeTypePut(pool, volName, volType, newdata)
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
