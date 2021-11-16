package main

import (
	"fmt"

	"github.com/spf13/cobra"

	// Used by cgo
	_ "github.com/lxc/lxd/lxd/include"
)

/*
#include "config.h"

#include <fcntl.h>
#include <libgen.h>
#include <sched.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <sys/types.h>
#include <unistd.h>

#include "lxd.h"
#include "memory_utils.h"
#include "mount_utils.h"
#include "syscall_numbers.h"
#include "syscall_wrappers.h"

void forkusernsexec(void)
{
	_exit(EXIT_SUCCESS);
}
*/
import "C"

type cmdForkusernsexec struct {
	global *cmdGlobal
}

func (c *cmdForkusernsexec) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkusernsexec <cmd> <args>"
	cmd.Short = "Run command in user namespace"
	cmd.Long = `Description:
  Run command in user namespace

  This command is used to execute a program in a new user namespace.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkusernsexec) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("This command should have been intercepted in cgo")
}

