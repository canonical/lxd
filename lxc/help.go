package main

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"

	"strings"
)

type helpCmd struct{}

const helpUsage = `
lxd help

Presents details on how to use lxd.
`

func (c *helpCmd) usage() string {
	return helpUsage
}

func (c *helpCmd) flags() {}

func (c *helpCmd) run(args []string) error {
	if len(args) > 0 {
		return errArgs
	}

	fmt.Println("Usage: lxc [subcommand] [options]\n")
	fmt.Println("Available commands:\n")
	var names []string
	for name, _ := range commands {
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

// summaryLine returns the first non-empty line immediately after the first
// line. Conventionally, this should be a one-line command summary, potentially
// followed by a longer explanation.
func summaryLine(usage string) string {
	usage = strings.TrimSpace(usage)
	s := bufio.NewScanner(bytes.NewBufferString(usage))
	if s.Scan() {
		for s.Scan() {
			if len(s.Text()) > 1 {
				return s.Text()
			}
		}
	}
	return "Missing summary."
}
