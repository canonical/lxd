package main

import (
	"errors"
	"fmt"
	"net"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdStorageVolumeNBD struct {
	global        *cmdGlobal
	storage       *cmdStorage
	storageVolume *cmdStorageVolume

	flagAddress  string
	flagReuse    bool
	flagWritable bool
}

func (c *cmdStorageVolumeNBD) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("nbd", "[<remote>:]<pool> [<type>/]<volume>")
	cmd.Short = "Serve a block storage volume over NBD"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

The command is not an NBD client. It opens a local listener, prints the address and forwards
the NBD client that connects to it to the LXD server, so that a tool such as nbdinfo, qemu-img
or nbdcopy can be pointed at it.

Without --reuse the command serves one client and exits when that client disconnects.
With --reuse it attaches to the session already open for the volume and keeps serving clients
while that session lasts. Once that session has ended the command reports the server's error
on stderr and exits successfully. With --writable it requests the import used for restore, which
needs the instance stopped. A writable export has no overlay, so --writable cannot be combined
with --reuse.`)
	cmd.Example = cli.FormatSection("", `lxc storage volume nbd default virtual-machine/vm1
    Serve the root disk of virtual machine "vm1" in pool "default" on a random loopback port.

lxc storage volume nbd default data --address /run/user/1000/data.sock
    Serve custom volume "data" in pool "default" on a unix socket.`)

	cmd.Flags().StringVar(&c.flagAddress, "address", "", cli.FormatStringFlagLabel("Local address to listen on, either host:port or an absolute unix socket path"))
	cmd.Flags().BoolVar(&c.flagReuse, "reuse", false, "Attach to the NBD session already open for the volume and keep serving clients")
	cmd.Flags().BoolVar(&c.flagWritable, "writable", false, "Request a writable export, which needs the instance stopped")
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

	if c.flagReuse && c.flagWritable {
		return errors.New("Cannot set --reuse with --writable, a writable export cannot be shared")
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

	return nbdProxy(listener, c.flagReuse, func() (net.Conn, error) {
		if c.flagWritable {
			return client.GetStoragePoolVolumeNBDWriteConn(resource.name, volType, volName, api.StorageVolumeNBDPost{})
		}

		return client.GetStoragePoolVolumeNBDConn(resource.name, volType, volName, api.StorageVolumeNBDGet{Reuse: c.flagReuse})
	})
}
