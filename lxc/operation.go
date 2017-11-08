package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/i18n"
)

type operationCmd struct {
}

func (c *operationCmd) showByDefault() bool {
	return true
}

func (c *operationCmd) usage() string {
	return i18n.G(
		`Usage: lxc operation <subcommand> [options]

List, show and delete background operations.

lxc operation list [<remote>:]
    List background operations.

lxc operation show [<remote>:]<operation>
    Show details on a background operation.

lxc operation delete [<remote>:]<operation>
    Delete a background operation (will attempt to cancel).

*Examples*
lxc operation show 344a79e4-d88a-45bf-9c39-c72c26f6ab8a
    Show details on that operation UUID`)
}

func (c *operationCmd) flags() {}

func (c *operationCmd) run(conf *config.Config, args []string) error {
	if len(args) < 1 {
		return errUsage
	}

	if args[0] == "list" {
		return c.doOperationList(conf, args)
	}

	if len(args) < 2 {
		return errArgs
	}

	remote, operation, err := conf.ParseRemote(args[1])
	if err != nil {
		return err
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	switch args[0] {
	case "delete":
		return c.doOperationDelete(client, operation)
	case "show":
		return c.doOperationShow(client, operation)
	default:
		return errArgs
	}
}

func (c *operationCmd) doOperationDelete(client lxd.ContainerServer, name string) error {
	err := client.DeleteOperation(name)
	if err != nil {
		return err
	}

	fmt.Printf(i18n.G("Operation %s deleted")+"\n", name)
	return nil
}

func (c *operationCmd) doOperationShow(client lxd.ContainerServer, name string) error {
	if name == "" {
		return errArgs
	}

	operation, _, err := client.GetOperation(name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&operation)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

func (c *operationCmd) doOperationList(conf *config.Config, args []string) error {
	var remote string
	var err error

	if len(args) > 1 {
		var name string
		remote, name, err = conf.ParseRemote(args[1])
		if err != nil {
			return err
		}

		if name != "" {
			return fmt.Errorf(i18n.G("Filtering isn't supported yet"))
		}
	} else {
		remote = conf.DefaultRemote
	}

	client, err := conf.GetContainerServer(remote)
	if err != nil {
		return err
	}

	operations, err := client.GetOperations()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, op := range operations {
		cancelable := i18n.G("NO")
		if op.MayCancel {
			cancelable = i18n.G("YES")
		}

		data = append(data, []string{op.ID, strings.ToUpper(op.Class), strings.ToUpper(op.Status), cancelable, op.CreatedAt.UTC().Format("2006/01/02 15:04 UTC")})
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetRowLine(true)
	table.SetHeader([]string{
		i18n.G("ID"),
		i18n.G("TYPE"),
		i18n.G("STATUS"),
		i18n.G("CANCELABLE"),
		i18n.G("CREATED")})
	sort.Sort(byName(data))
	table.AppendBulk(data)
	table.Render()

	return nil
}
