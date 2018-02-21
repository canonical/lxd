package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/eagain"
)

// Netcat is called with:
//
//    lxd netcat /path/to/unix/socket
//
// and does unbuffered netcatting of to socket to stdin/stdout. Any arguments
// after the path to the unix socket are ignored, so that this can be passed
// directly to rsync as the sync command.
func cmdNetcat(args *Args) error {
	if len(args.Params) < 2 {
		return fmt.Errorf("Bad arguments %q", args.Params)
	}

	logPath := shared.LogPath(args.Params[1], "netcat.log")
	if shared.PathExists(logPath) {
		os.Remove(logPath)
	}

	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_SYNC, 0644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	uAddr, err := net.ResolveUnixAddr("unix", args.Params[0])
	if err != nil {
		logFile.WriteString(fmt.Sprintf("Could not resolve unix domain socket \"%s\": %s.\n", args.Params[0], err))
		return err
	}

	conn, err := net.DialUnix("unix", nil, uAddr)
	if err != nil {
		logFile.WriteString(fmt.Sprintf("Could not dial unix domain socket \"%s\": %s.\n", args.Params[0], err))
		return err
	}

	wg := sync.WaitGroup{}
	wg.Add(1)

	go func() {
		_, err := io.Copy(eagain.Writer{Writer: os.Stdout}, eagain.Reader{Reader: conn})
		if err != nil {
			logFile.WriteString(fmt.Sprintf("Error while copying from stdout to unix domain socket \"%s\": %s.\n", args.Params[0], err))
		}
		conn.Close()
		wg.Done()
	}()

	go func() {
		_, err := io.Copy(eagain.Writer{Writer: conn}, eagain.Reader{Reader: os.Stdin})
		if err != nil {
			logFile.WriteString(fmt.Sprintf("Error while copying from unix domain socket \"%s\" to stdin: %s.\n", args.Params[0], err))
		}
	}()

	wg.Wait()

	return nil
}
