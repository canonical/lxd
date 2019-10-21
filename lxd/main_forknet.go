package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/shared/netutils"
)

/*
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <errno.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>
#include <unistd.h>

extern char *advance_arg(bool required);
extern int dosetns(int pid, char *nstype);
extern int dosetns_file(char *file, char *nstype);

void forkdonetinfo(pid_t pid) {
	if (dosetns(pid, "net") < 0) {
		fprintf(stderr, "Failed setns to container network namespace: %s\n", strerror(errno));
		_exit(1);
	}

	// Jump back to Go for the rest
}

void forkdonetdetach(char *file) {
	if (dosetns_file(file, "net") < 0) {
		fprintf(stderr, "Failed setns to container network namespace: %s\n", strerror(errno));
		_exit(1);
	}

	// Jump back to Go for the rest
}

void forknet() {
	char *command = NULL;
	char *cur = NULL;
	pid_t pid = 0;


	// Get the subcommand
	command = advance_arg(false);
	if (command == NULL || (strcmp(command, "--help") == 0 || strcmp(command, "--version") == 0 || strcmp(command, "-h") == 0)) {
		return;
	}

	// Get the pid
	cur = advance_arg(false);
	if (cur == NULL || (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0)) {
		return;
	}

	// Check that we're root
	if (geteuid() != 0) {
		fprintf(stderr, "Error: forknet requires root privileges\n");
		_exit(1);
	}

	// Call the subcommands
	if (strcmp(command, "info") == 0) {
		pid = atoi(cur);
		forkdonetinfo(pid);
	}

	if (strcmp(command, "detach") == 0)
		forkdonetdetach(cur);
}
*/
import "C"
import "github.com/lxc/lxd/shared"

type cmdForknet struct {
	global *cmdGlobal
}

func (c *cmdForknet) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forknet"
	cmd.Short = "Perform container network operations"
	cmd.Long = `Description:
  Perform container network operations

  This set of internal commands are used for some container network
  operations which require attaching to the container's network namespace.
`
	cmd.Hidden = true

	// pull
	cmdInfo := &cobra.Command{}
	cmdInfo.Use = "info <PID>"
	cmdInfo.Args = cobra.ExactArgs(1)
	cmdInfo.RunE = c.RunInfo
	cmd.AddCommand(cmdInfo)

	// detach
	cmdDetach := &cobra.Command{}
	cmdDetach.Use = "detach <netns file> <LXD PID> <ifname> <hostname>"
	cmdDetach.Args = cobra.ExactArgs(4)
	cmdDetach.RunE = c.RunDetach
	cmd.AddCommand(cmdDetach)

	return cmd
}

func (c *cmdForknet) RunInfo(cmd *cobra.Command, args []string) error {
	networks, err := netutils.NetnsGetifaddrs(-1)
	if err != nil {
		return err
	}

	buf, err := json.Marshal(networks)
	if err != nil {
		return err
	}

	fmt.Printf("%s\n", buf)

	return nil
}

func (c *cmdForknet) RunDetach(cmd *cobra.Command, args []string) error {
	lxdPID := args[1]
	ifName := args[2]
	hostName := args[3]

	if lxdPID == "" {
		return fmt.Errorf("LXD PID argument is required")
	}

	if ifName == "" {
		return fmt.Errorf("ifname argument is required")
	}

	if hostName == "" {
		return fmt.Errorf("hostname argument is required")
	}

	// Remove all IP addresses from interface before moving to parent netns.
	// This is to avoid any container address config leaking into host.
	_, err := shared.RunCommand("ip", "address", "flush", "dev", ifName)
	if err != nil {
		return err
	}

	// Rename the interface, set it down, and move into parent netns.
	_, err = shared.RunCommand("ip", "link", "set", ifName, "down", "name", hostName, "netns", lxdPID)
	if err != nil {
		return err
	}

	return nil
}
