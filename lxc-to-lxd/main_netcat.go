package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/shared/eagain"
)

type cmdNetcat struct {
	global *cmdGlobal
}

// Defines a Cobra command for sending stdin data to a specified unix socket via netcat.
func (c *cmdNetcat) Command() *cobra.Command {
	cmd := &cobra.Command{}

	cmd.Use = "netcat <address>"
	cmd.Short = "Sends stdin data to a unix socket"
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

// Executes the netcat command for data transfer to a Unix socket, handling errors and synchronization.
func (c *cmdNetcat) Run(cmd *cobra.Command, args []string) error {
	// Help and usage
	if len(args) == 0 {
		_ = cmd.Help()
		return nil
	}

	// Handle mandatory arguments
	if len(args) != 1 {
		_ = cmd.Help()
		return fmt.Errorf("Missing required argument")
	}

	// Connect to the provided address
	uAddr, err := net.ResolveUnixAddr("unix", args[0])
	if err != nil {
		return err
	}

	conn, err := net.DialUnix("unix", nil, uAddr)
	if err != nil {
		return err
	}

	// We'll wait until we're done reading from the socket
	wg := sync.WaitGroup{}
	wg.Add(1)

	go func() {
		defer func() { _ = conn.Close() }()
		defer wg.Done()

		_, _ = io.Copy(eagain.Writer{Writer: os.Stdout}, eagain.Reader{Reader: conn})
	}()

	go func() {
		_, _ = io.Copy(eagain.Writer{Writer: conn}, eagain.Reader{Reader: os.Stdin})
	}()

	// Wait
	wg.Wait()

	return nil
}
