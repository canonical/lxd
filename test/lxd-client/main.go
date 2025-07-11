package main

/*
 * A small LXD client used to test specific edge cases.
 */

import (
	"fmt"
	"os"
	"path"
	"strings"

	lxdClient "github.com/canonical/lxd/client"
)

type commandFunc func(client lxdClient.InstanceServer, args []string) error

var commands = map[string]commandFunc{
	"file-push": cmdInstanceFilePush,
}

// cmdInstanceFilePush creates a file on the instance with an optional string content.
func cmdInstanceFilePush(client lxdClient.InstanceServer, args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return fmt.Errorf("Usage: %s file-push <instName> <instFilePath> [<content>]", path.Base(os.Args[0]))
	}

	instName := args[0]
	instFilePath := args[1]

	contentString := ""
	if len(args) == 3 {
		contentString = args[2]
	}

	instFileArgs := lxdClient.InstanceFileArgs{
		Content: strings.NewReader(contentString),
		Mode:    0755,
	}

	return client.CreateInstanceFile(instName, instFilePath, instFileArgs)
}

func run(args []string) error {
	client, err := lxdClient.ConnectLXDUnix("", nil)
	if err != nil {
		return err
	}

	defer client.Disconnect()

	cmdName := ""
	if len(args) > 1 {
		cmdName = args[1]
	}

	// Find an run command.
	for name, run := range commands {
		if name == cmdName {
			return run(client, args[2:])
		}
	}

	// Command not found, show available commands.
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}

	return fmt.Errorf("Unknown command %q\n\nAvailable commands:\n%s", cmdName, strings.Join(names, "\n"))
}

func main() {
	err := run(os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
