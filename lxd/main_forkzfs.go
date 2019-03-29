package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

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
	// Sanity checks
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

	// Unshare a clean mount namespace
	err := syscall.Unshare(syscall.CLONE_NEWNS)
	if err != nil {
		return err
	}

	// Mark mount tree as private
	err = syscall.Mount("none", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, "")
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

		syscall.Unmount(rows[4], syscall.MNT_DETACH)
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
