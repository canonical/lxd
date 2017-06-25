package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

type helpCmd struct {
	showAll bool
}

func (c *helpCmd) showByDefault() bool {
	return true
}

func (c *helpCmd) usage() string {
	return i18n.G(
		`Usage: lxc help [--all]

Help page for the LXD client.`)
}

func (c *helpCmd) flags() {
	gnuflag.BoolVar(&c.showAll, "all", false, i18n.G("Show all commands (not just interesting ones)"))
}

func (c *helpCmd) run(conf *config.Config, args []string) error {
	if len(args) > 0 {
		for _, name := range args {
			cmd, ok := commands[name]
			if !ok {
				fmt.Fprintf(os.Stderr, i18n.G("error: unknown command: %s")+"\n", name)
			} else {
				fmt.Fprintf(os.Stdout, cmd.usage()+"\n")
			}
		}
		return nil
	}

	fmt.Println(i18n.G("Usage: lxc <command> [options]"))
	fmt.Println()
	fmt.Println(i18n.G(`This is the LXD command line client.

All of LXD's features can be driven through the various commands below.
For help with any of those, simply call them with --help.`))
	fmt.Println()

	fmt.Println(i18n.G("Commands:"))
	var names []string
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "help" {
			continue
		}

		cmd := commands[name]
		if c.showAll || cmd.showByDefault() {
			fmt.Printf("  %-16s %s\n", name, summaryLine(cmd.usage()))
		}
	}

	fmt.Println()
	fmt.Println(i18n.G("Options:"))
	fmt.Println("  --all            " + i18n.G("Print less common commands"))
	fmt.Println("  --debug          " + i18n.G("Print debug information"))
	fmt.Println("  --verbose        " + i18n.G("Print verbose information"))
	fmt.Println("  --version        " + i18n.G("Show client version"))
	fmt.Println()
	fmt.Println(i18n.G("Environment:"))
	fmt.Println("  LXD_CONF         " + i18n.G("Path to an alternate client configuration directory"))
	fmt.Println("  LXD_DIR          " + i18n.G("Path to an alternate server directory"))

	return nil
}
