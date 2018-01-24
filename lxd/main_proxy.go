package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
)

func cmdProxyDevStart(args *Args) error {
	if len(args.Params) != 8 {
		return fmt.Errorf("Invalid number of arguments")
	}

	// Get all our arguments
	listenPid := args.Params[0]
	listenAddr := args.Params[1]
	connectPid := args.Params[2]
	connectAddr := args.Params[3]

	fd := -1
	if args.Params[4] != "0" {
		fd, _ = strconv.Atoi(args.Params[4])
	}

	// At this point we have already forked and should set this flag to 1
	args.Params[5] = "1"

	// Check where we are in initialization
	if !shared.PathExists(fmt.Sprintf("/proc/self/fd/%d", fd)) {
		fmt.Printf("Listening on %s in %s, forwarding to %s from %s\n", listenAddr, listenPid, connectAddr, connectPid)

		file, err := getListenerFile(listenAddr)
		if err != nil {
			return err
		}
		defer file.Close()

		listenerFd := file.Fd()
		if err != nil {
			return fmt.Errorf("failed to duplicate the listener fd: %v", err)
		}

		newFd, err := syscall.Dup(int(listenerFd))
		if err != nil {
			return fmt.Errorf("failed to dup fd: %v", err)
		}

		fmt.Printf("Re-executing proxy process\n")

		args.Params[4] = strconv.Itoa(int(newFd))
		execArgs := append([]string{"lxd", "forkproxy"}, args.Params...)

		err = syscall.Exec(util.GetExecPath(), execArgs, os.Environ())
		if err != nil {
			return fmt.Errorf("failed to re-exec: %v", err)
		}
	}

	// Re-create listener from fd
	listenFile := os.NewFile(uintptr(fd), "listener")
	listener, err := net.FileListener(listenFile)
	if err != nil {
		return fmt.Errorf("failed to re-assemble listener: %v", err)
	}

	defer listener.Close()

	fmt.Printf("Starting to proxy\n")

	// begin proxying
	for {
		// Accept a new client
		srcConn, err := listener.Accept()
		if err != nil {
			fmt.Printf("error: Failed to accept new connection: %v\n", err)
			continue
		}
		fmt.Printf("Accepted a new connection\n")

		// Connect to the target
		dstConn, err := getDestConn(connectAddr)
		if err != nil {
			fmt.Printf("error: Failed to connect to target: %v\n", err)
			srcConn.Close()
			continue
		}

		go io.Copy(srcConn, dstConn)
		go io.Copy(dstConn, srcConn)
	}
}

func getListenerFile(listenAddr string) (os.File, error) {
	fields := strings.SplitN(listenAddr, ":", 2)
	addr := strings.Join(fields[1:], "")

	listener, err := net.Listen(fields[0], addr)
	if err != nil {
		return os.File{}, err
	}

	file := &os.File{}
	switch listener.(type) {
	case *net.TCPListener:
		tcpListener := listener.(*net.TCPListener)
		file, err = tcpListener.File()
	case *net.UnixListener:
		unixListener := listener.(*net.UnixListener)
		file, err = unixListener.File()
	}

	if err != nil {
		return os.File{}, fmt.Errorf("Failed to get file from listener: %v", err)
	}

	return *file, nil
}

func getDestConn(connectAddr string) (net.Conn, error) {
	fields := strings.SplitN(connectAddr, ":", 2)
	addr := strings.Join(fields[1:], "")
	return net.Dial(fields[0], addr)
}
