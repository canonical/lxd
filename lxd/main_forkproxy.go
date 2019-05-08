package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/eagain"
	"github.com/lxc/lxd/shared/netutils"
)

/*
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

extern char* advance_arg(bool required);
extern void attach_userns(int pid);
extern int dosetns(int pid, char *nstype);

int whoami = -ESRCH;

#define FORKPROXY_CHILD 1
#define FORKPROXY_PARENT 0
#define FORKPROXY_UDS_SOCK_FD_NUM 200

static int wait_for_pid(pid_t pid)
{
	int status, ret;

again:
	ret = waitpid(pid, &status, 0);
	if (ret == -1) {
		if (errno == EINTR)
			goto again;
		return -1;
	}
	if (ret != pid)
		goto again;
	if (!WIFEXITED(status) || WEXITSTATUS(status) != 0)
		return -1;
	return 0;
}

void forkproxy()
{
	int connect_pid, listen_pid, log_fd;
	ssize_t ret;
	pid_t pid;
	char *connect_addr, *cur, *listen_addr, *log_path, *pid_path;
	int sk_fds[2] = {-EBADF, -EBADF};
	FILE *pid_file;

	// Get the pid
	cur = advance_arg(false);
	if (cur == NULL ||
	    (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 ||
	     strcmp(cur, "-h") == 0))
		_exit(EXIT_FAILURE);

	listen_pid = atoi(cur);
	listen_addr = advance_arg(true);
	connect_pid = atoi(advance_arg(true));
	connect_addr = advance_arg(true);
	log_path = advance_arg(true);
	pid_path = advance_arg(true);

	close(STDIN_FILENO);
	log_fd = open(log_path, O_WRONLY | O_CREAT | O_CLOEXEC | O_TRUNC, 0600);
	if (log_fd < 0)
		_exit(EXIT_FAILURE);

	ret = dup3(log_fd, STDOUT_FILENO, O_CLOEXEC);
	if (ret < 0)
		_exit(EXIT_FAILURE);

	ret = dup3(log_fd, STDERR_FILENO, O_CLOEXEC);
	if (ret < 0)
		_exit(EXIT_FAILURE);

	pid_file = fopen(pid_path, "we+");
	if (!pid_file) {
		fprintf(stderr,
			"%s - Failed to create pid file for proxy daemon\n",
			strerror(errno));
		_exit(EXIT_FAILURE);
	}

	ret = socketpair(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0, sk_fds);
	if (ret < 0) {
		fprintf(stderr,
			"%s - Failed to create anonymous unix socket pair\n",
			strerror(errno));
		_exit(EXIT_FAILURE);
	}

	pid = fork();
	if (pid < 0) {
		fprintf(stderr, "%s - Failed to create new process\n",
			strerror(errno));
		_exit(EXIT_FAILURE);
	}

	if (pid == 0) {
		whoami = FORKPROXY_CHILD;

		fclose(pid_file);
		ret = close(sk_fds[0]);
		if (ret < 0)
			fprintf(stderr, "%s - Failed to close fd %d\n",
				strerror(errno), sk_fds[0]);

		// Attach to the user namespace of the listener
		attach_userns(listen_pid);

		// Attach to the network namespace of the listener
		ret = dosetns(listen_pid, "net");
		if (ret < 0) {
			fprintf(stderr, "Failed setns to listener network namespace: %s\n",
				strerror(errno));
			_exit(EXIT_FAILURE);
		}

		// Attach to the mount namespace of the listener
		ret = dosetns(listen_pid, "mnt");
		if (ret < 0) {
			fprintf(stderr, "Failed setns to listener mount namespace: %s\n",
				strerror(errno));
			_exit(EXIT_FAILURE);
		}

		ret = dup3(sk_fds[1], FORKPROXY_UDS_SOCK_FD_NUM, O_CLOEXEC);
		if (ret < 0) {
			fprintf(stderr,
				"%s - Failed to duplicate fd %d to fd 200\n",
				strerror(errno), sk_fds[1]);
			_exit(EXIT_FAILURE);
		}

		ret = close(sk_fds[1]);
		if (ret < 0)
			fprintf(stderr, "%s - Failed to close fd %d\n",
				strerror(errno), sk_fds[1]);
	} else {
		whoami = FORKPROXY_PARENT;

		ret = close(sk_fds[1]);
		if (ret < 0)
			fprintf(stderr, "%s - Failed to close fd %d\n",
				strerror(errno), sk_fds[1]);

		// Attach to the user namespace of the listener
		attach_userns(connect_pid);

		// Attach to the network namespace of the listener
		ret = dosetns(connect_pid, "net");
		if (ret < 0) {
			fprintf(stderr, "Failed setns to listener network namespace: %s\n",
				strerror(errno));
			_exit(EXIT_FAILURE);
		}

		// Attach to the mount namespace of the listener
		ret = dosetns(connect_pid, "mnt");
		if (ret < 0) {
			fprintf(stderr, "Failed setns to listener mount namespace: %s\n",
				strerror(errno));
			_exit(EXIT_FAILURE);
		}

		ret = dup3(sk_fds[0], FORKPROXY_UDS_SOCK_FD_NUM, O_CLOEXEC);
		if (ret < 0) {
			fprintf(stderr,
				"%s - Failed to duplicate fd %d to fd 200\n",
				strerror(errno), sk_fds[1]);
			_exit(EXIT_FAILURE);
		}

		ret = close(sk_fds[0]);
		if (ret < 0)
			fprintf(stderr, "%s - Failed to close fd %d\n",
				strerror(errno), sk_fds[0]);

		// Usually we should wait for the child process somewhere here.
		// But we cannot really do this. The listener file descriptors
		// are retrieved in the go runtime but at that point we have
		// already double-fork()ed to daemonize ourselves and so we
		// can't wait on the child anymore after we received the
		// listener fds. On the other hand, if we wait on the child
		// here we wait on the child before the receive. However, if we
		// do this then we can end up in a situation where the socket
		// send buffer is full and we need to retrieve some file
		// descriptors first before we can go on sending more. But this
		// won't be possible because we're waiting before the call to
		// receive the file descriptor in the go runtime. Luckily, we
		// can just rely on init doing it's job and reaping the zombie
		// process. So, technically unsatisfying but pragmatically
		// correct.

		// daemonize
		pid = fork();
		if (pid < 0)
			_exit(EXIT_FAILURE);

		if (pid != 0) {
			ret = wait_for_pid(pid);
			if (ret < 0)
				_exit(EXIT_FAILURE);

			_exit(EXIT_SUCCESS);
		}

		pid = fork();
		if (pid < 0)
			_exit(EXIT_FAILURE);

		if (pid != 0) {
			ret = fprintf(pid_file, "%d", pid);
			fclose(pid_file);
			if (ret < 0) {
				fprintf(stderr, "Failed to write proxy daemon pid %d to \"%s\"\n",
					pid, pid_path);
				ret = EXIT_FAILURE;
			}
			close(STDOUT_FILENO);
			close(STDERR_FILENO);
			_exit(EXIT_SUCCESS);
		}

		ret = setsid();
		if (ret < 0)
			fprintf(stderr, "%s - Failed to setsid in proxy daemon\n",
				strerror(errno));
	}
}
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
import "C"

const forkproxyUDSSockFDNum int = C.FORKPROXY_UDS_SOCK_FD_NUM

type cmdForkproxy struct {
	global *cmdGlobal
}

type proxyAddress struct {
	connType string
	addr     string
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
	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	// Sanity checks
	if len(args) != 6 {
		cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	// Get all our arguments
	listenAddr := args[1]

	// Check where we are in initialization
	if C.whoami != C.FORKPROXY_PARENT && C.whoami != C.FORKPROXY_CHILD {
		return fmt.Errorf("Failed to call forkproxy constructor")
	}

	lAddr := proxyParseAddr(listenAddr)

	if C.whoami == C.FORKPROXY_CHILD {
		err := os.Remove(lAddr.addr)
		if err != nil && !os.IsNotExist(err) {
			return err
		}

		file, err := getListenerFile(listenAddr)
		if err != nil {
			return err
		}
	sAgain:
		err = netutils.AbstractUnixSendFd(forkproxyUDSSockFDNum, int(file.Fd()))
		if err != nil {
			errno, ok := shared.GetErrno(err)
			if ok && (errno == syscall.EAGAIN) {
				goto sAgain
			}
		}
		syscall.Close(forkproxyUDSSockFDNum)

		file.Close()
		return err
	}

rAgain:
	file, err := netutils.AbstractUnixReceiveFd(forkproxyUDSSockFDNum)
	if err != nil {
		errno, ok := shared.GetErrno(err)
		if ok && (errno == syscall.EAGAIN) {
			goto rAgain
		}

		fmt.Printf("Failed to receive fd from listener process: %v\n", err)
		syscall.Close(forkproxyUDSSockFDNum)
		return err
	}

	syscall.Close(forkproxyUDSSockFDNum)

	listener, err := net.FileListener(file)
	if err != nil {
		fmt.Printf("Failed to re-assemble listener: %v", err)
		return err
	}

	// Handle SIGTERM which is sent when the proxy is to be removed
	terminate := false
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)

	// Wait for SIGTERM and close the listener in order to exit the loop below
	go func() {
		<-sigs
		terminate = true
		listener.Close()
	}()

	connectAddr := args[3]

	fmt.Printf("Starting to proxy\n")

	// begin proxying
	for {
		// Accept a new client
		srcConn, err := listener.Accept()
		if err != nil {
			if terminate {
				break
			}

			fmt.Printf("error: Failed to accept new connection: %v\n", err)
			continue
		}

		// Connect to the target
		dstConn, err := getDestConn(connectAddr)
		if err != nil {
			fmt.Printf("error: Failed to connect to target: %v\n", err)
			srcConn.Close()
			continue
		}

		go genericRelay(srcConn, dstConn)
	}

	fmt.Printf("Stopping proxy\n")

	return nil
}

func genericRelay(dst io.ReadWriteCloser, src io.ReadWriteCloser) {
	relayer := func(src io.Writer, dst io.Reader, ch chan error) {
		_, err := io.Copy(eagain.Writer{Writer: src}, eagain.Reader{Reader: dst})
		ch <- err
		close(ch)
	}

	chSend := make(chan error)
	go relayer(dst, src, chSend)

	chRecv := make(chan error)
	go relayer(src, dst, chRecv)

	select {
	case errSnd := <-chSend:
		if errSnd != nil {
			fmt.Printf("Error while sending data %s\n", errSnd)
		}

	case errRcv := <-chRecv:
		if errRcv != nil {
			fmt.Printf("Error while reading data %s\n", errRcv)
		}
	}

	src.Close()
	dst.Close()

	// Empty the channels
	<-chSend
	<-chRecv
}

func tryListen(protocol string, addr string) (net.Listener, error) {
	var listener net.Listener
	var err error

	for i := 0; i < 10; i++ {
		listener, err = net.Listen(protocol, addr)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		return nil, err
	}

	return listener, nil
}

func getListenerFile(listenAddr string) (os.File, error) {
	fields := strings.SplitN(listenAddr, ":", 2)
	addr := strings.Join(fields[1:], "")

	listener, err := tryListen(fields[0], addr)
	if err != nil {
		return os.File{}, fmt.Errorf("Failed to listen on %s: %v", addr, err)
	}

	file := &os.File{}
	switch listener.(type) {
	case *net.TCPListener:
		tcpListener := listener.(*net.TCPListener)
		file, err = tcpListener.File()
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

func proxyParseAddr(addr string) *proxyAddress {
	fields := strings.SplitN(addr, ":", 2)
	return &proxyAddress{
		connType: fields[0],
		addr:     fields[1],
	}
}
