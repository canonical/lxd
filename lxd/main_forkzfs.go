package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared"
)

type cmdForkZFS struct {
	global *cmdGlobal
}

func (c *cmdForkZFS) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkzfs [<arguments>...]"
	cmd.Short = "Run ZFS inside a cleaned up mount namepsace"
	cmd.Long = `Description:
  Run ZFS inside a cleaned up mount namepsace

  This internal command is used to run ZFS in some specific cases.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkZFS) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	if len(args) < 1 {
		cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	// Mark mount tree as private
	err := unix.Mount("none", "/", "", unix.MS_REC|unix.MS_PRIVATE, "")
	if err != nil {
		return err
	}

	// Expand the mount path
	absPath, err := filepath.Abs(shared.VarPath())
	if err != nil {
		return err
	}

	expPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		expPath = absPath
	}

	// Find the source mount of the path
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	defer file.Close()

	// Unmount all mounts under LXD directory
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		rows := strings.Fields(line)

		if !strings.HasPrefix(rows[4], expPath) {
			continue
		}

		unix.Unmount(rows[4], unix.MNT_DETACH)
	}

	// Run the ZFS command
	command := exec.Command("zfs", args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	err = command.Run()
	if err != nil {
		return err
	}

	return nil
}
