package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/eagain"
)

/*
#define _GNU_SOURCE
#include <errno.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

extern char* advance_arg(bool required);
extern int dosetns(int pid, char *nstype);

void forkproxy() {
	char *cur = NULL;

	int cmdline, listen_pid, connect_pid, fdnum, forked, childPid, ret;
	char *logPath = NULL, *pidPath = NULL;
	FILE *logFile = NULL, *pidFile = NULL;

	// /proc/self/fd/<num> (14 (path) + 21 (int64) + 1 (null))
	char fdpath[36];

	// Get the pid
	cur = advance_arg(false);
	if (cur == NULL || (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0)) {
		return;
	}
	listen_pid = atoi(cur);

	// Get the arguments
	advance_arg(true);
	connect_pid = atoi(advance_arg(true));
	advance_arg(true);
	fdnum = atoi(advance_arg(true));
	forked = atoi(advance_arg(true));
	logPath = advance_arg(true);
	pidPath = advance_arg(true);

	// Check if proxy daemon already forked
	if (forked == 0) {
		logFile = fopen(logPath, "w+");
		if (logFile == NULL) {
			_exit(1);
		}

		if (dup2(fileno(logFile), STDOUT_FILENO) < 0) {
			fprintf(logFile, "Failed to redirect STDOUT to logfile: %s\n", strerror(errno));
			_exit(1);
		}
		if (dup2(fileno(logFile), STDERR_FILENO) < 0) {
			fprintf(logFile, "Failed to redirect STDERR to logfile: %s\n", strerror(errno));
			_exit(1);
		}
		fclose(logFile);

		pidFile = fopen(pidPath, "w+");
		if (pidFile == NULL) {
			fprintf(stderr, "Failed to create pid file for proxy daemon: %s\n", strerror(errno));
			_exit(1);
		}

		childPid = fork();
		if (childPid < 0) {
			fprintf(stderr, "Failed to fork proxy daemon: %s\n", strerror(errno));
			_exit(1);
		} else if (childPid != 0) {
			fprintf(pidFile, "%d", childPid);
			fclose(pidFile);
			fclose(stdin);
			fclose(stdout);
			fclose(stderr);
			_exit(0);
		} else {
			ret = setsid();
			if (ret < 0) {
				fprintf(stderr, "Failed to setsid in proxy daemon: %s\n", strerror(errno));
				_exit(1);
			}
		}
	}

	// Cannot pass through -1 to runCommand since it is interpreted as a flag
	fdnum = fdnum == 0 ? -1 : fdnum;

	ret = snprintf(fdpath, sizeof(fdpath), "/proc/self/fd/%d", fdnum);
	if (ret < 0 || (size_t)ret >= sizeof(fdpath)) {
		fprintf(stderr, "Failed to format file descriptor path\n");
		_exit(1);
	}

	// Join the listener ns if not already setup
	if (access(fdpath, F_OK) < 0) {
		// Attach to the network namespace of the listener
		if (dosetns(listen_pid, "net") < 0) {
			fprintf(stderr, "Failed setns to listener network namespace: %s\n", strerror(errno));
			_exit(1);
		}
	} else {
		// Join the connector ns now
		if (dosetns(connect_pid, "net") < 0) {
			fprintf(stderr, "Failed setns to connector network namespace: %s\n", strerror(errno));
			_exit(1);
		}
	}
}
*/
import "C"

type cmdForkproxy struct {
	global *cmdGlobal
}

func (c *cmdForkproxy) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkproxy <listen PID> <listen address> <connect PID> <connect address> <fd> <reexec> <log path> <pid path>"
	cmd.Short = "Setup network connection proxying"
	cmd.Long = `Description:
  Setup network connection proxying

  This internal command will spawn a new proxy process for a particular
  container, connecting one side to the host and the other to the
  container.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkproxy) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	if len(args) != 8 {
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

	// Get all our arguments
	listenPid := args[0]
	listenAddr := args[1]
	connectPid := args[2]
	connectAddr := args[3]

	fd := -1
	if args[4] != "0" {
		fd, _ = strconv.Atoi(args[4])
	}

	// At this point we have already forked and should set this flag to 1
	args[5] = "1"

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
			return fmt.Errorf("Failed to duplicate the listener fd: %v", err)
		}

		newFd, err := syscall.Dup(int(listenerFd))
		if err != nil {
			return fmt.Errorf("Failed to dup fd: %v", err)
		}

		fmt.Printf("Re-executing proxy process\n")

		args[4] = strconv.Itoa(int(newFd))
		execArgs := append([]string{"lxd", "forkproxy"}, args...)

		err = syscall.Exec(util.GetExecPath(), execArgs, os.Environ())
		if err != nil {
			return fmt.Errorf("Failed to re-exec: %v", err)
		}
	}

	// Re-create listener from fd
	listenFile := os.NewFile(uintptr(fd), "listener")
	listener, err := net.FileListener(listenFile)
	if err != nil {
		return fmt.Errorf("Failed to re-assemble listener: %v", err)
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

		go io.Copy(eagain.Writer{Writer: srcConn}, eagain.Reader{Reader: dstConn})
		go io.Copy(eagain.Writer{Writer: dstConn}, eagain.Reader{Reader: srcConn})
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
