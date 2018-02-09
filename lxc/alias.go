package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/olekukonko/tablewriter"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/i18n"
)

type aliasCmd struct {
}

func (c *aliasCmd) showByDefault() bool {
	return true
}

func (c *aliasCmd) usage() string {
	return i18n.G(
		`Usage: lxc alias <subcommand> [options]

Manage command aliases.

lxc alias add <alias> <target>
    Add a new alias <alias> pointing to <target>.

lxc alias remove <alias>
    Remove the alias <alias>.

lxc alias list
    List all the aliases.

lxc alias rename <old alias> <new alias>
    Rename remote <old alias> to <new alias>.`)
}

func (c *aliasCmd) flags() {
}

func (c *aliasCmd) run(conf *config.Config, args []string) error {
	if len(args) < 1 {
		return errUsage
	}

	switch args[0] {
	case "add":
		if len(args) != 3 {
			return errArgs
		}

		_, ok := conf.Aliases[args[1]]
		if ok {
			return fmt.Errorf(i18n.G("alias %s already exists"), args[1])
		}

		conf.Aliases[args[1]] = args[2]

	case "remove":
		if len(args) != 2 {
			return errArgs
		}

		_, ok := conf.Aliases[args[1]]
		if !ok {
			return fmt.Errorf(i18n.G("alias %s doesn't exist"), args[1])
		}

		delete(conf.Aliases, args[1])

	case "list":
		data := [][]string{}
		for k, v := range conf.Aliases {
			data = append(data, []string{k, v})
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetAutoWrapText(false)
		table.SetAlignment(tablewriter.ALIGN_LEFT)
		table.SetRowLine(true)
		table.SetHeader([]string{
			i18n.G("ALIAS"),
			i18n.G("TARGET")})
		sort.Sort(byName(data))
		table.AppendBulk(data)
		table.Render()

	case "rename":
		if len(args) != 3 {
			return errArgs
		}

		target, ok := conf.Aliases[args[1]]
		if !ok {
			return fmt.Errorf(i18n.G("alias %s doesn't exist"), args[1])
		}

		_, ok = conf.Aliases[args[2]]
		if ok {
			return fmt.Errorf(i18n.G("alias %s already exists"), args[2])
		}

		conf.Aliases[args[2]] = target
		delete(conf.Aliases, args[1])
	}

	return conf.SaveConfig(configPath)
}
