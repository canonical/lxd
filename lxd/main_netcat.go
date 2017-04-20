package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
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
		io.Copy(eagainWriter{os.Stdout}, eagainReader{conn})
		conn.Close()
		wg.Done()
	}()

	go func() {
		io.Copy(conn, os.Stdin)
	}()

	wg.Wait()

	return nil
}

type eagainReader struct {
	r io.Reader
}

func (er eagainReader) Read(p []byte) (int, error) {
	// keep retrying on EAGAIN
again:
	n, err := er.r.Read(p)
	if err == nil {
		return n, nil
	}

	var errno error
	// EAGAIN errors can hide in os.PathError. My best explanation for this
	// weirdness is that os.Stdout refers to /dev/stdout which is a path.
	sysErr, ok := err.(*os.PathError)
	if ok {
		errno = sysErr.Err
	} else {
		tmpErrno, ok := err.(syscall.Errno)
		if ok {
			errno = tmpErrno
		}
	}

	if errno == syscall.EAGAIN {
		goto again
	}

	return n, err
}

type eagainWriter struct {
	w io.Writer
}

func (ew eagainWriter) Write(p []byte) (int, error) {
	// keep retrying on EAGAIN
again:
	n, err := ew.w.Write(p)
	if err == nil {
		return n, nil
	}

	var errno error
	// EAGAIN errors can hide in os.PathError. My best explanation for this
	// weirdness is that os.Stdout refers to /dev/stdout which is a path.
	sysErr, ok := err.(*os.PathError)
	if ok {
		errno = sysErr.Err
	} else {
		tmpErrno, ok := err.(syscall.Errno)
		if ok {
			errno = tmpErrno
		}
	}
	if errno == syscall.EAGAIN {
		goto again
	}

	return n, err
}
