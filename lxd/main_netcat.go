package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"

	"github.com/lxc/lxd/shared"
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
		_, err := io.Copy(eagainWriter{os.Stdout}, eagainReader{conn})
		if err != nil {
			logFile.WriteString(fmt.Sprintf("Error while copying from stdout to unix domain socket \"%s\": %s.\n", args.Params[0], err))
		}
		conn.Close()
		wg.Done()
	}()

	go func() {
		_, err := io.Copy(eagainWriter{conn}, eagainReader{os.Stdin})
		if err != nil {
			logFile.WriteString(fmt.Sprintf("Error while copying from unix domain socket \"%s\" to stdin: %s.\n", args.Params[0], err))
		}
	}()

	wg.Wait()

	return nil
}

type eagainReader struct {
	r io.Reader
}

func (er eagainReader) Read(p []byte) (int, error) {
again:
	n, err := er.r.Read(p)
	if err == nil {
		return n, nil
	}

	// keep retrying on EAGAIN
	errno, ok := shared.GetErrno(err)
	if ok && errno == syscall.EAGAIN {
		goto again
	}

	return n, err
}

type eagainWriter struct {
	w io.Writer
}

func (ew eagainWriter) Write(p []byte) (int, error) {
again:
	n, err := ew.w.Write(p)
	if err == nil {
		return n, nil
	}

	// keep retrying on EAGAIN
	errno, ok := shared.GetErrno(err)
	if ok && errno == syscall.EAGAIN {
		goto again
	}

	return n, err
}
