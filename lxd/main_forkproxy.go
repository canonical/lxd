package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/eagain"
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

int wait_for_pid(pid_t pid)
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

ssize_t lxc_read_nointr(int fd, void* buf, size_t count)
{
	ssize_t ret;
again:
	ret = read(fd, buf, count);
	if (ret < 0 && errno == EINTR)
		goto again;
	return ret;
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

	pid_file = fopen(pid_path, "w+");
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
	fprintf(stderr, "Created anonymous pair {%d,%d} of unix sockets\n",
		sk_fds[0], sk_fds[1]);

	pid = fork();
	if (pid < 0) {
		fprintf(stderr, "%s - Failed to create new process\n",
			strerror(errno));
		_exit(EXIT_FAILURE);
	}

	if (pid == 0) {
		whoami = FORKPROXY_CHILD;

		fclose(pid_file);
		close(sk_fds[0]);

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
			_exit(1);
		}

		ret = close(sk_fds[1]);
		if (ret < 0) {
			fprintf(stderr, "%s - Failed to close socket fd %d\n",
				strerror(errno), sk_fds[1]);
			_exit(1);
		}
	} else {
		whoami = FORKPROXY_PARENT;

		close(sk_fds[1]);

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
			_exit(1);
		}

		ret = close(sk_fds[0]);
		if (ret < 0) {
			fprintf(stderr, "%s - Failed to close socket fd %d\n",
				strerror(errno), sk_fds[1]);
			_exit(1);
		}

		ret = wait_for_pid(pid);
		if (ret < 0) {
			fprintf(stderr, "Failed to start listener\n");
			_exit(EXIT_FAILURE);
		}

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
import "C"

const forkproxyUDSSockFDNum int = C.FORKPROXY_UDS_SOCK_FD_NUM

type cmdForkproxy struct {
	global *cmdGlobal
}

type proxyAddress struct {
	connType string
	addr     []string
	abstract bool
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

	lAddr, err := parseAddr(listenAddr)
	if err != nil {
		return err
	}

	if C.whoami == C.FORKPROXY_CHILD {
		if lAddr.connType == "unix"  && !lAddr.abstract {
			err := os.Remove(lAddr.addr[0])
			if err != nil && !os.IsNotExist(err) {
				return err
			}
		}

		for _, port := range lAddr.addr {
			fmt.Println(port)
		}

		for _, addr := range lAddr.addr {
			file, err := getListenerFile(lAddr.connType, addr)
			if err != nil {
				return err
			}

			err = shared.AbstractUnixSendFd(forkproxyUDSSockFDNum, int(file.Fd()))
			file.Close()
			if err != nil {
				break
			}
		}

		syscall.Close(forkproxyUDSSockFDNum)
		return err
	}

	files := []*os.File{}
	for range lAddr.addr {
		f, err := shared.AbstractUnixReceiveFd(forkproxyUDSSockFDNum)
		if err != nil {
			fmt.Printf("Failed to receive fd from listener process: %v\n", err)
			return err
		}
		files = append(files, f)
	}
	syscall.Close(forkproxyUDSSockFDNum)

	var srcConn net.Conn
	var listeners []*net.Listener

	udpFD := -1
	if lAddr.connType == "udp" {
		udpFD = int(files[0].Fd())
		srcConn, err = net.FileConn(files[0])
		if err != nil {
			fmt.Printf("Failed to re-assemble listener: %v", err)
			return err
		}
	} else {
		for _, f := range files {
			listener, err := net.FileListener(f)
			if err != nil {
				fmt.Printf("Failed to re-assemble listener: %v", err)
				return err
			}
			listeners = append(listeners, &listener)
		}
	}

	// Handle SIGTERM which is sent when the proxy is to be removed
	terminate := false
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)

	// Wait for SIGTERM and close the listener in order to exit the loop below
	killOnUDP := syscall.Getpid()
	go func() {
		<-sigs
		terminate = true

		for _, f := range files {
			f.Close()
		}

		if lAddr.connType == "udp" {
			srcConn.Close()
			// Kill ourselves since we will otherwise block on UDP
			// connect() or poll().
			syscall.Kill(killOnUDP, syscall.SIGKILL)
		} else {
			for _, listener := range listeners {
				(*listener).Close()
			}
		}
	}()

	connectAddr := args[3]
	cAddr, err := parseAddr(connectAddr)
	if err != nil {
		return err
	}

	if cAddr.connType == "unix" && !cAddr.abstract {
		// Create socket
		file, err := getListenerFile("unix", cAddr.addr[0])
		if err != nil {
			return err
		}
		file.Close()

		if cAddr.connType == "unix" && !cAddr.abstract {
			defer os.Remove(cAddr.addr[0])
		}
	}

	if lAddr.connType == "unix" && !lAddr.abstract {
		defer os.Remove(lAddr.addr[0])
	}

	fmt.Printf("Starting %s to %s proxy\n", lAddr.connType, cAddr.connType)
	if lAddr.connType == "udp" {
		for {
			ret, revents, err := shared.GetPollRevents(udpFD, -1, (shared.POLLIN | shared.POLLPRI | shared.POLLERR | shared.POLLHUP | shared.POLLRDHUP | shared.POLLNVAL))
			if ret < 0 {
				fmt.Printf("Failed to poll on file descriptor: %s\n", err)
				srcConn.Close()
				return err
			}

			if (revents & (shared.POLLERR | shared.POLLHUP | shared.POLLRDHUP | shared.POLLNVAL)) > 0 {
				err := fmt.Errorf("Invalid UDP socket file descriptor")
				fmt.Printf("%s\n", err)
				srcConn.Close()
				return err
			}

			// Connect to the target
			dstConn, err := getDestConn(connectAddr)
			if err != nil {
				fmt.Printf("error: Failed to connect to target: %v\n", err)
				srcConn.Close()
				return err
			}

			genericRelay(srcConn, dstConn, false)
		}
	} else {
		// begin proxying
		for {
			// Accept a new client
			srcConn, err = (*listeners[0]).Accept()
			if err != nil {
				if terminate {
					break
				}

				fmt.Printf("error: Failed to accept new connection: %v\n", err)
				continue
			}
			fmt.Printf("Accepted a new connection\n")

			// Connect to the target
			dstConn, err := getDestConn(connectAddr)
			if err != nil {
				fmt.Printf("error: Failed to connect to target: %v\n", err)
				if lAddr.connType != "udp" {
					srcConn.Close()
				}

				continue
			}

			if cAddr.connType == "unix" && lAddr.connType == "unix" {
				// Handle OOB if both src and dst are using unix sockets
				go unixRelay(srcConn, dstConn)
			} else {
				go genericRelay(srcConn, dstConn, true)
			}
		}
	}

	fmt.Printf("Stopping proxy\n")

	return nil
}

func genericRelay(dst io.ReadWriteCloser, src io.ReadWriteCloser, closeDst bool) {
	relayer := func(dst io.Writer, src io.Reader, ch chan error) {
		_, err := io.Copy(eagain.Writer{Writer: dst}, eagain.Reader{Reader: src})
		ch <- err
	}

	chSend := make(chan error)
	go relayer(dst, src, chSend)

	chRecv := make(chan error)
	go relayer(src, dst, chRecv)

	errSnd := <-chSend
	errRcv := <-chRecv

	src.Close()
	if closeDst {
		dst.Close()
	}

	if errSnd != nil {
		fmt.Printf("Error while sending data %s\n", errSnd)
	}

	if errRcv != nil {
		fmt.Printf("Error while reading data %s\n", errRcv)
	}
}

func unixRelayer(src *net.UnixConn, dst *net.UnixConn, ch chan bool) {
	dataBuf := make([]byte, 4096)
	oobBuf := make([]byte, 4096)

	for {
		// Read from the source
	readAgain:
		sData, sOob, _, _, err := src.ReadMsgUnix(dataBuf, oobBuf)
		if err != nil {
			errno, ok := shared.GetErrno(err)
			if ok && errno == syscall.EAGAIN {
				goto readAgain
			}
			fmt.Printf("Disconnected during read: %v\n", err)
			ch <- true
			return
		}

		var fds []int
		if sOob > 0 {
			entries, err := syscall.ParseSocketControlMessage(oobBuf[:sOob])
			if err != nil {
				fmt.Printf("Failed to parse control message: %v\n", err)
				ch <- true
				return
			}

			for _, msg := range entries {
				fds, err = syscall.ParseUnixRights(&msg)
				if err != nil {
					fmt.Printf("Failed to get fd list for control message: %v\n", err)
					ch <- true
					return
				}
			}
		}

		// Send to the destination
	writeAgain:
		tData, tOob, err := dst.WriteMsgUnix(dataBuf[:sData], oobBuf[:sOob], nil)
		if err != nil {
			errno, ok := shared.GetErrno(err)
			if ok && errno == syscall.EAGAIN {
				goto writeAgain
			}
			fmt.Printf("Disconnected during write: %v\n", err)
			ch <- true
			return
		}

		if sData != tData || sOob != tOob {
			fmt.Printf("Some data got lost during transfer, disconnecting.")
			ch <- true
			return
		}

		// Close those fds we received
		if fds != nil {
			for _, fd := range fds {
				err := syscall.Close(fd)
				if err != nil {
					fmt.Printf("Failed to close fd %d: %v\n", fd, err)
					ch <- true
					return
				}
			}
		}
	}
}

func unixRelay(dst io.ReadWriteCloser, src io.ReadWriteCloser) {
	chSend := make(chan bool)
	go unixRelayer(dst.(*net.UnixConn), src.(*net.UnixConn), chSend)

	chRecv := make(chan bool)
	go unixRelayer(src.(*net.UnixConn), dst.(*net.UnixConn), chRecv)

	<-chSend
	<-chRecv

	src.Close()
	dst.Close()
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

func tryListenUDP(protocol string, addr string) (*os.File, error) {
	var UDPConn *net.UDPConn
	var err error

	udpAddr, err := net.ResolveUDPAddr(protocol, addr)
	if err != nil {
		return nil, err
	}

	for i := 0; i < 10; i++ {
		UDPConn, err = net.ListenUDP(protocol, udpAddr)
		if err == nil {
			file, err := UDPConn.File()
			UDPConn.Close()
			return file, err
		}

		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		return nil, err
	}

	if UDPConn == nil {
		return nil, fmt.Errorf("Failed to setup UDP listener")
	}

	file, err := UDPConn.File()
	UDPConn.Close()
	return file, err
}

func getListenerFile(protocol string, addr string) (*os.File, error) {
	if protocol == "udp" {
		return tryListenUDP("udp", addr)
	}

	listener, err := tryListen(protocol, addr)
	if err != nil {
		return nil, fmt.Errorf("Failed to listen on %s: %v", addr, err)
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
		return nil, fmt.Errorf("Failed to get file from listener: %v", err)
	}

	return file, nil
}

func getDestConn(connectAddr string) (net.Conn, error) {
	fields := strings.SplitN(connectAddr, ":", 2)
	addr := strings.Join(fields[1:], "")
	return net.Dial(fields[0], addr)
}

func parsePortRange(r string) (int64, int64, error) {
	entries := strings.Split(r, "-")
	if len(entries) > 2 {
		return -1, -1, fmt.Errorf("Invalid port range %s", r)
	}

	base, err := strconv.ParseInt(entries[0], 10, 64)
	if err != nil {
		return -1, -1, err
	}

	size := int64(1)
	if len(entries) > 1 {
		size, err = strconv.ParseInt(entries[1], 10, 64)
		if err != nil {
			return -1, -1, err
		}

		size -= base
		size += 1
	}

	return base, size, nil
}

func parseAddr(addr string) (*proxyAddress, error) {
	// Split into <protocol> and <address>
	fields := strings.SplitN(addr, ":", 2)

	newProxyAddr := &proxyAddress{
		connType: fields[0],
		abstract: strings.HasPrefix(fields[1], "@"),
	}

	// unix addresses cannot have ports
	if newProxyAddr.connType == "unix" {
		newProxyAddr.addr = []string{fields[1]}
		return newProxyAddr, nil
	}

	// Split <address> into <address> and <ports>
	addrParts := strings.SplitN(fields[1], ":", 2)
	// no ports
	if len(addrParts) == 1 {
		newProxyAddr.addr = []string{fields[1]}
		return newProxyAddr, nil
	}

	// Split <ports> into individual ports and port ranges
	ports := strings.SplitN(addrParts[1], ",", -1)
	for _, port := range ports {
		portFirst, portRange, err := parsePortRange(port)
		if err != nil {
			return nil, err
		}

		for i := int64(0); i < portRange; i++ {
			newAddr := fmt.Sprintf("%s:%d", addrParts[0], portFirst + i)
			newProxyAddr.addr = append(newProxyAddr.addr, newAddr)
		}
	}

	return newProxyAddr, nil
}
