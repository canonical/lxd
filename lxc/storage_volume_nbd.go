package main

import (
	"errors"
	"fmt"
	"net"

	"github.com/spf13/cobra"

	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdStorageVolumeNBD struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagAddress  string
	flagWritable bool
}

func (c *cmdStorageVolumeNBD) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("nbd", "[<remote>:]<pool> [<type>/]<volume>")
	cmd.Short = "Serve a block storage volume over NBD"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

The volume is served read-write under the default export, for restoring a backup. The bitmaps of
the volume are deleted first. The virtual machine that uses the volume must be stopped. The command
requires --writable to confirm that the volume is overwritten.

The command opens a local listener, prints the address, and forwards the connection of the NBD
client to the LXD server. Point a tool such as nbdinfo, qemu-img or nbdcopy at the printed
address. The command serves one client and exits when that client disconnects.`)
	cmd.Example = cli.FormatSection("", `lxc storage volume nbd default virtual-machine/vm1 --writable
    Serve the root volume of virtual machine "vm1" in pool "default" on a random loopback port.

lxc storage volume nbd default data --writable --address /run/user/1000/data.sock
    Serve custom volume "data" in pool "default" on a unix socket.`)

	cmd.Flags().StringVar(&c.flagAddress, "address", "", cli.FormatStringFlagLabel("Local address to listen on, either host:port or an absolute unix socket path"))
	cmd.Flags().BoolVar(&c.flagWritable, "writable", false, "Confirm that the volume is served read-write and its bitmaps are deleted")
	cmd.Flags().StringVar(&c.storage.flagTarget, "target", "", cli.FormatStringFlagLabel("Cluster member name"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpTopLevelResource("storage_pool", toComplete)
		}

		if len(args) == 1 {
			return c.global.cmpStoragePoolVolumes(args[0])
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdStorageVolumeNBD) run(cmd *cobra.Command, args []string) error {
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
		return errors.New("Missing pool name")
	}

	if !c.flagWritable {
		return errors.New("The volume is served read-write, which --writable confirms")
	}

	client := resource.server

	// If a target member was specified, serve the volume from that member.
	if c.storage.flagTarget != "" {
		client = client.UseTarget(c.storage.flagTarget)
	}

	// Parse the input
	volName, volType := parseVolume("custom", args[1])

	// Check that the volume exists before opening the listener.
	_, _, err = client.GetStoragePoolVolume(resource.name, volType, volName)
	if err != nil {
		return err
	}

	listener, err := nbdListen(c.flagAddress)
	if err != nil {
		return err
	}

	defer func() { _ = listener.Close() }()

	fmt.Printf("NBD listening on %v\n", listener.Addr())

	return nbdProxy(listener, func() (net.Conn, error) {
		conn, _, err := client.GetStoragePoolVolumeNBDConn(resource.name, volType, volName)

		return conn, err
	})
}
