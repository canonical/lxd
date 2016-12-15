package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"
)

// Netcat is called with:
//
//    lxd netcat /path/to/unix/socket
//
// and does unbuffered netcatting of to socket to stdin/stdout. Any arguments
// after the path to the unix socket are ignored, so that this can be passed
// directly to rsync as the sync command.
func cmdNetcat(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("Bad arguments %q", args)
	}

	uAddr, err := net.ResolveUnixAddr("unix", args[1])
	if err != nil {
		return err
	}

	conn, err := net.DialUnix("unix", nil, uAddr)
	if err != nil {
		return err
	}

	wg := sync.WaitGroup{}
	wg.Add(1)

	go func() {
		io.Copy(os.Stdout, conn)
		conn.Close()
		wg.Done()
	}()

	go func() {
		io.Copy(conn, os.Stdin)
	}()

	wg.Wait()

	return nil
}
