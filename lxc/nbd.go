package main

import (
	"errors"
	"fmt"
	"net"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdNBD struct {
	global *cmdGlobal

	flagAddress string
	flagReuse   bool
}

func (c *cmdNBD) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("nbd", "[<remote>:]<instance>")
	cmd.Short = "Serve every block disk of an instance over NBD"
	cmd.Long = cli.FormatSection("Description", cmd.Short+`

Every non-shared block disk of a running virtual machine is served under an export named
after its LXD device name, so that the NBD client picks the disk during the handshake.

The command is not an NBD client. It opens a local listener, prints the address and forwards
the NBD client that connects to it to the LXD server, so that a tool such as nbdinfo, qemu-img
or nbdcopy can be pointed at it.

Without --reuse the command serves one client and exits when that client disconnects.
With --reuse it attaches to the session already open for the instance and keeps serving clients
while that session lasts. Once that session has ended the command reports the server's error
on stderr and exits successfully.`)
	cmd.Example = cli.FormatSection("", `lxc nbd vm1
    Serve every block disk of virtual machine "vm1" on a random loopback port.

lxc nbd vm1 --address 127.0.0.1:10809
    Serve every block disk of virtual machine "vm1" on port 10809.`)

	cmd.Flags().StringVar(&c.flagAddress, "address", "", cli.FormatStringFlagLabel("Local address to listen on, either host:port or an absolute unix socket path"))
	cmd.Flags().BoolVar(&c.flagReuse, "reuse", false, "Attach to the NBD session already open for the instance and keep serving clients")
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpTopLevelResource("instance", toComplete)
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

	if resource.name == "" {
		return errors.New("Missing instance name")
	}

	// Check that the instance exists before opening the listener.
	_, _, err = resource.server.GetInstance(resource.name)
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
		return resource.server.GetInstanceNBDConn(resource.name, api.InstanceNBDGet{Reuse: c.flagReuse})
	})
}
