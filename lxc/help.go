package main

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"

	"strings"

	"github.com/lxc/lxd"
)

type helpCmd struct{}

const helpUsage = `
Presents details on how to use lxd.

lxd help
`

func (c *helpCmd) usage() string {
	return helpUsage
}

func (c *helpCmd) flags() {}

func (c *helpCmd) run(_ *lxd.Config, args []string) error {
	if len(args) > 0 {
		return errArgs
	}

	fmt.Println("Usage: lxc [subcommand] [options]")
	fmt.Println("Available commands:")
	var names []string
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cmd := commands[name]
		fmt.Printf("\t%-10s - %s\n", name, summaryLine(cmd.usage()))
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
	return "Missing summary."
}
