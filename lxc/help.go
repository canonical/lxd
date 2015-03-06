package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
)

type helpCmd struct{}

func (c *helpCmd) showByDefault() bool {
	return true
}

func (c *helpCmd) usage() string {
	return gettext.Gettext(
		"Presents details on how to use LXD.\n" +
			"\n" +
			"lxd help [--all]\n")
}

var showAll bool

func (c *helpCmd) flags() {
	gnuflag.BoolVar(&showAll, "all", false, gettext.Gettext("Show all commands (not just interesting ones)"))
}

func (c *helpCmd) run(_ *lxd.Config, args []string) error {
	if len(args) > 0 {
		for _, name := range args {
			cmd, ok := commands[name]
			if !ok {
				fmt.Fprintf(os.Stderr, gettext.Gettext("error: unknown command: %s\n"), name)
			} else {
				fmt.Fprintf(os.Stderr, cmd.usage())
			}
		}
		return nil
	}

	fmt.Println(gettext.Gettext("Usage: lxc [subcommand] [options]\nAvailable commands:\n"))
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
	fmt.Println()
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
	return gettext.Gettext("Missing summary.")
}
