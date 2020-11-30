package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/eagain"
)

type cmdNetcat struct {
	global *cmdGlobal
}

func (c *cmdNetcat) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "netcat <address> <name>"
	cmd.Short = "Send stdin data to a unix socket"
	cmd.Long = `Description:
  Send stdin data to a unix socket

  This internal command is used to forward the output of a program over
  a websocket by first forwarding it to a unix socket controlled by LXD.

  Its main use is when running rsync or btrfs/zfs send/receive between
  two machines over the LXD websocket API.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdNetcat) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	if len(args) < 2 {
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

	logPath := shared.LogPath(args[1], "netcat.log")
	if shared.PathExists(logPath) {
		os.Remove(logPath)
	}

	logFile, logErr := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_SYNC, 0644)
	if logErr == nil {
		defer logFile.Close()
	}

	uAddr, err := net.ResolveUnixAddr("unix", args[0])
	if err != nil {
		if logErr == nil {
			logFile.WriteString(fmt.Sprintf("Could not resolve unix domain socket \"%s\": %s\n", args[0], err))
		}
		return err
	}

	conn, err := net.DialUnix("unix", nil, uAddr)
	if err != nil {
		if logErr == nil {
			logFile.WriteString(fmt.Sprintf("Could not dial unix domain socket \"%s\": %s\n", args[0], err))
		}
		return err
	}

	wg := sync.WaitGroup{}
	wg.Add(1)

	go func() {
		_, err := io.Copy(eagain.Writer{Writer: os.Stdout}, eagain.Reader{Reader: conn})
		if err != nil && logErr == nil {
			logFile.WriteString(fmt.Sprintf("Error while copying from stdout to unix domain socket \"%s\": %s\n", args[0], err))
		}
		conn.Close()
		wg.Done()
	}()

	go func() {
		_, err := io.Copy(eagain.Writer{Writer: conn}, eagain.Reader{Reader: os.Stdin})
		if err != nil && logErr == nil {
			logFile.WriteString(fmt.Sprintf("Error while copying from unix domain socket \"%s\" to stdin: %s\n", args[0], err))
		}
	}()

	wg.Wait()

	return nil
}
