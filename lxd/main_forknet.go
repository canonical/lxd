package main

/*
#include "config.h"

#include <errno.h>
#include <fcntl.h>
#include <sched.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#include "lxd.h"
#include "macro.h"
#include "memory_utils.h"

static void forkdonetinfo(int pidfd, int ns_fd)
{
	if (!change_namespaces(pidfd, ns_fd, CLONE_NEWNET)) {
		fprintf(stderr, "Failed setns to container network namespace: %s\n", strerror(errno));
		_exit(1);
	}

	// Jump back to Go for the rest
}

static int dosetns_file(char *file, char *nstype)
{
	__do_close int ns_fd = -EBADF;

	ns_fd = open(file, O_RDONLY);
	if (ns_fd < 0) {
		fprintf(stderr, "%m - Failed to open \"%s\"", file);
		return -1;
	}

	if (setns(ns_fd, 0) < 0) {
		fprintf(stderr, "%m - Failed to attach to namespace \"%s\"", file);
		return -1;
	}

	return 0;
}

static void forkdonetdetach(char *file) {
	if (dosetns_file(file, "net") < 0) {
		fprintf(stderr, "Failed setns to container network namespace: %s\n", strerror(errno));
		_exit(1);
	}

	// Jump back to Go for the rest
}

void forknet(void)
{
	char *command = NULL;
	char *cur = NULL;
	pid_t pid = 0;


	// Get the subcommand
	command = advance_arg(false);
	if (command == NULL || (strcmp(command, "--help") == 0 || strcmp(command, "--version") == 0 || strcmp(command, "-h") == 0)) {
		return;
	}

	// skip "--"
	advance_arg(true);

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
		int ns_fd, pidfd;
		pid = atoi(cur);

		pidfd = atoi(advance_arg(true));
		ns_fd = pidfd_nsfd(pidfd, pid);
		if (ns_fd < 0)
			_exit(1);

		forkdonetinfo(pidfd, ns_fd);
	}

	if (strcmp(command, "detach") == 0)
		forkdonetdetach(cur);
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/spf13/cobra"

	_ "github.com/canonical/lxd/lxd/include" // Used by cgo
	"github.com/canonical/lxd/lxd/ip"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/netutils"
)

type cmdForknet struct {
	global *cmdGlobal
}

// Sets up and runs hidden commands for container network operations like info and detach.
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
	cmdInfo.Use = "info <PID> <PidFd>"
	cmdInfo.Args = cobra.ExactArgs(2)
	cmdInfo.RunE = c.RunInfo
	cmd.AddCommand(cmdInfo)

	// detach
	cmdDetach := &cobra.Command{}
	cmdDetach.Use = "detach <netns file> <LXD PID> <ifname> <hostname>"
	cmdDetach.Args = cobra.ExactArgs(4)
	cmdDetach.RunE = c.RunDetach
	cmd.AddCommand(cmdDetach)

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Fetches and prints container network interfaces information.
func (c *cmdForknet) RunInfo(cmd *cobra.Command, args []string) error {
	hostInterfaces, _ := net.Interfaces()
	networks, err := netutils.NetnsGetifaddrs(-1, hostInterfaces)
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

// Detaches a network interface from a container, removing IP addresses and moving it to the host namespace.
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
	addr := &ip.Addr{
		DevName: ifName,
	}

	err := addr.Flush()
	if err != nil {
		return err
	}

	// Set interface down, rename it, and move into parent netns.
	link := &ip.Link{Name: ifName}
	err = link.SetDown()
	if err != nil {
		return err
	}

	err = link.SetName(hostName)
	if err != nil {
		// If the interface has an altname that matches the target name, this can prevent rename of the
		// interface, so try removing it and trying the rename again if succeeds.
		_, altErr := shared.RunCommand("ip", "link", "property", "del", "dev", ifName, "altname", hostName)
		if altErr == nil {
			err = link.SetName(hostName)
		}

		return err
	}

	link = &ip.Link{Name: hostName}
	err = link.SetNetns(lxdPID)
	if err != nil {
		return err
	}

	return nil
}
