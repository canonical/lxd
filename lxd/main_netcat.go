package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"sync"
)

type logErrorReader struct {
	r io.Reader
	f *os.File
}

func (le logErrorReader) Read(p []byte) (int, error) {
	n, err := le.r.Read(p)
	if err != nil {
		fmt.Fprintf(le.f, "Error reading netcat stream: %v", err)
	}

	return n, err
}

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

	f, err := ioutil.TempFile("", "lxd_netcat_")
	if err != nil {
		return err
	}
	defer f.Close()

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
		_, err := io.Copy(os.Stdout, logErrorReader{conn, f})
		if err != nil {
			fmt.Fprintf(f, "Error netcatting to stdout: %v", err)
		}
		conn.Close()
		wg.Done()
	}()

	go func() {
		_, err := io.Copy(conn, logErrorReader{conn, os.Stdin})
		if err != nil {
			fmt.Fprintf(f, "Error netcatting to conn: %v", err)
		}
	}()

	wg.Wait()

	return nil
}
