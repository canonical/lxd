package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/i18n"
	"github.com/lxc/lxd/shared/gnuflag"
)

type helpCmd struct{}

func (c *helpCmd) showByDefault() bool {
	return true
}

func (c *helpCmd) usage() string {
	return i18n.G(
		`Presents details on how to use LXD.

lxd help [--all]`)
}

var showAll bool

func (c *helpCmd) flags() {
	gnuflag.BoolVar(&showAll, "all", false, i18n.G("Show all commands (not just interesting ones)"))
}

func (c *helpCmd) run(_ *lxd.Config, args []string) error {
	if len(args) > 0 {
		for _, name := range args {
			cmd, ok := commands[name]
			if !ok {
				fmt.Fprintf(os.Stderr, i18n.G("error: unknown command: %s")+"\n", name)
			} else {
				fmt.Fprintf(os.Stderr, cmd.usage()+"\n")
			}
		}
		return nil
	}

	fmt.Println(i18n.G("Usage: lxc [subcommand] [options]"))
	fmt.Println(i18n.G("Available commands:"))
	var names []string
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cmd := commands[name]
		if showAll || cmd.showByDefault() {
			fmt.Printf("\t%-10s - %s\n", name, summaryLine(cmd.usage()))
		}
	}
	if !showAll {
		fmt.Println()
		fmt.Println(i18n.G("Options:"))
		fmt.Println("  --all              " + i18n.G("Print less common commands."))
		fmt.Println("  --debug            " + i18n.G("Print debug information."))
		fmt.Println("  --verbose          " + i18n.G("Print verbose information."))
		fmt.Println()
		fmt.Println(i18n.G("Environment:"))
		fmt.Println("  LXD_CONF           " + i18n.G("Path to an alternate client configuration directory."))
		fmt.Println("  LXD_DIR            " + i18n.G("Path to an alternate server directory."))
	}
	return nil
}

// summaryLine returns the first line of the help text. Conventionally, this
// should be a one-line command summary, potentially followed by a longer
// explanation.
func summaryLine(usage string) string {
	usage = strings.TrimSpace(usage)
	s := bufio.NewScanner(bytes.NewBufferString(usage))
	if s.Scan() {
		if len(s.Text()) > 1 {
			return s.Text()
		}
	}
	return i18n.G("Missing summary.")
}
