package main

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdNBD struct {
	global *cmdGlobal

	flagAddress              string
	flagDevices              string
	flagPreviousSnapshotUUID string
}

func (c *cmdNBD) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("nbd", "[<remote>:]<instance>/<snapshot>")
	cmd.Short = "Serve the block volume snapshots of an instance snapshot over NBD"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

Every block volume snapshot of the instance snapshot is served as a separate NBD export, together
with the bitmaps of the instance snapshot as "qemu:dirty-bitmap:<name>" metadata contexts. During
the handshake, the NBD client selects a volume by its disk device name at the time of the snapshot.
The instance snapshot must have been created with a bitmap.

The command opens a local listener, prints the address, and forwards the connection of the NBD
client to the LXD server. Point a tool such as nbdinfo, qemu-img or nbdcopy at the printed
address. The command serves one client and exits when that client disconnects.`)
	cmd.Example = cli.FormatSection("", `lxc nbd vm1/snap1
    Serve every block volume snapshot of snapshot "snap1" of virtual machine "vm1" on a random loopback port.

lxc nbd vm1/snap1 --devices root --address 127.0.0.1:10809
    Serve the root volume snapshot of snapshot "snap1" of virtual machine "vm1" on port 10809.`)

	cmd.Flags().StringVar(&c.flagAddress, "address", "", cli.FormatStringFlagLabel("Local address to listen on, either host:port or an absolute unix socket path"))
	cmd.Flags().StringVar(&c.flagDevices, "devices", "", cli.FormatStringFlagLabel("Comma separated names of the disk devices whose volume snapshots are served, all of them by default"))
	cmd.Flags().StringVar(&c.flagPreviousSnapshotUUID, "previous-snapshot-uuid", "", cli.FormatStringFlagLabel("UUID of the previous snapshot, which limits the served bitmaps to the ones created with it"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpInstancesAndSnapshots(toComplete)
	}

	return cmd
}

func (c *cmdNBD) run(cmd *cobra.Command, args []string) error {
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

	instName, snapName, isSnapshot := api.GetParentAndSnapshotName(resource.name)
	if !isSnapshot {
		return errors.New("Missing instance snapshot name")
	}

	deviceNames := []string{}
	if c.flagDevices != "" {
		deviceNames = strings.Split(c.flagDevices, ",")
	}

	// Check that the snapshot exists before opening the listener.
	_, _, err = resource.server.GetInstanceSnapshot(instName, snapName)
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
		return resource.server.GetInstanceSnapshotNBDConn(instName, snapName, deviceNames, c.flagPreviousSnapshotUUID)
	})
}
