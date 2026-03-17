// Analogous to main_forksyscall.go but for cases, when
// you want to implement stuff in Golang.
package main

import (
	"bytes"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
)

type cmdForksyscallgo struct {
	global *cmdGlobal
}

// Command returns a cobra.Command object representing the "forksyscallgo" command.
func (c *cmdForksyscallgo) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forksyscallgo <syscall_operation> <PID> <PidFd> [...]"
	cmd.Short = "Perform syscall operations (golang)"
	cmd.Long = `Description:
  Perform syscall operations (golang)

  This set of internal commands is used for all seccomp-based container syscall
  operations (golang).
`
	cmd.Hidden = true

	// finit_module_parse
	finitModuleParseCmd := cmdFinitModuleParse{global: c.global, syscallgo: c}
	cmd.AddCommand(finitModuleParseCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// finit_module_parse.
type cmdFinitModuleParse struct {
	global    *cmdGlobal
	syscallgo *cmdForksyscallgo
}

// Command returns a cobra.Command object representing the "finit_module_parse" subcommand.
func (c *cmdFinitModuleParse) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "finit_module_parse <module_fd>"

	cmd.RunE = c.Run

	return cmd
}

// Run executes the "finit_module_parse" subcommand.
func (c *cmdFinitModuleParse) Run(cmd *cobra.Command, args []string) error {
	moduleFD, err := strconv.Atoi(args[2])
	if err != nil {
		return fmt.Errorf("Cannot extract module_fd: %w", err)
	}

	f := os.NewFile(uintptr(moduleFD), "/proc/self/fd/<module_fd>")
	if f == nil {
		return fmt.Errorf("Cannot open module file: %w", err)
	}

	defer func() { _ = f.Close() }()

	elfFile, err := elf.NewFile(f)
	if err != nil {
		return fmt.Errorf("elf.NewFile failed: %w", err)
	}

	sec := elfFile.Section(".modinfo")
	if sec == nil {
		return errors.New("module's ELF file has no .modinfo section")
	}

	secData, err := sec.Data()
	if err != nil {
		return fmt.Errorf("Cannot read .modinfo section: %w", err)
	}

	_, secNameStart, ok := bytes.Cut(secData, []byte("name="))
	if !ok {
		return errors.New(`.modinfo section data looks wrong: cannot find "name="`)
	}

	if len(secNameStart) == 0 {
		return errors.New(`.modinfo section data looks wrong: no data after "name="`)
	}

	secName, _, ok := bytes.Cut(secNameStart, []byte("\x00"))
	if !ok {
		return errors.New(".modinfo section data looks wrong: cannot find terminating NULL-byte")
	}

	if len(secName) == 0 {
		return errors.New(".modinfo section data looks wrong: module name is empty")
	}

	// print extracted module name so we can use it in the seccomp.go
	fmt.Printf("%s", string(secName))

	return nil
}
