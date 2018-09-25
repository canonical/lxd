package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/shared"
)

/*
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/epoll.h>
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

int switch_uid_gid(uint32_t uid, uint32_t gid)
{
	if (setgid((gid_t)gid) < 0)
		return -1;

	if (setuid((uid_t)uid) < 0)
		return -1;

	return 0;
}

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

	if (strncmp(listen_addr, "udp:", sizeof("udp:") - 1) == 0 &&
	    strncmp(connect_addr, "udp:", sizeof("udp:") - 1) != 0) {
		    fprintf(stderr, "Error: Proxying from udp to non-udp "
			    "protocol is not supported\n");
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
			_exit(1);
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
			_exit(1);
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
	addr     []string
	abstract bool
}

// UDP session tracking (map "client tuple" to udp session)
var udpSessions = map[string]*udpSession{}
var udpSessionsLock sync.Mutex

type udpSession struct {
	client net.Addr
	target net.Conn
	timer  *time.Timer
}

func (c *cmdForkproxy) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkproxy <listen PID> <listen address> <connect PID> <connect address> <log path> <pid path> <listen gid> <listen uid> <listen mode> <security gid> <security uid>"
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

func rearmUDPFd(epFd C.int, connFd C.int) {
	var ev C.struct_epoll_event
	ev.events = C.EPOLLIN | C.EPOLLONESHOT
	*(*C.int)(unsafe.Pointer(uintptr(unsafe.Pointer(&ev)) + unsafe.Sizeof(ev.events))) = connFd
	ret := C.epoll_ctl(epFd, C.EPOLL_CTL_MOD, connFd, &ev)
	if ret < 0 {
		fmt.Printf("Error: Failed to add listener fd to epoll instance\n")
	}
}

func listenerInstance(epFd C.int, lAddr *proxyAddress, cAddr *proxyAddress, connFd C.int, lStruct *lStruct, proxy bool) error {
	if lAddr.connType == "udp" {
		// This only handles udp <-> udp. The C constructor will have
		// verified this before.
		go func() {
			// single or multiple port -> single port
			connectAddr := cAddr.addr[0]
			if len(cAddr.addr) > 1 {
				// multiple port -> multiple port
				connectAddr = cAddr.addr[(*lStruct).lAddrIndex]
			}

			srcConn, err := net.FileConn((*lStruct).f)
			if err != nil {
				fmt.Printf("Failed to re-assemble listener: %s", err)
				rearmUDPFd(epFd, connFd)
				return
			}

			dstConn, err := net.Dial(cAddr.connType, connectAddr)
			if err != nil {
				fmt.Printf("Error: Failed to connect to target: %v\n", err)
				rearmUDPFd(epFd, connFd)
				return
			}

			genericRelay(srcConn, dstConn, true)
			rearmUDPFd(epFd, connFd)
		}()

		return nil
	}

	// Accept a new client
	listener := (*lStruct).lConn
	srcConn, err := (*listener).Accept()
	if err != nil {
		fmt.Printf("Error: Failed to accept new connection: %v\n", err)
		return err
	}

	// single or multiple port -> single port
	connectAddr := cAddr.addr[0]
	if lAddr.connType != "unix" && cAddr.connType != "unix" && len(cAddr.addr) > 1 {
		// multiple port -> multiple port
		connectAddr = cAddr.addr[(*lStruct).lAddrIndex]
	}
	dstConn, err := net.Dial(cAddr.connType, connectAddr)
	if err != nil {
		srcConn.Close()
		fmt.Printf("Error: Failed to connect to target: %v\n", err)
		return err
	}

	if proxy && cAddr.connType == "tcp" {
		if lAddr.connType == "unix" {
			dstConn.Write([]byte(fmt.Sprintf("PROXY UNKNOWN\r\n")))
		} else {
			cHost, cPort, err := net.SplitHostPort(srcConn.RemoteAddr().String())
			if err != nil {
				return err
			}

			dHost, dPort, err := net.SplitHostPort(srcConn.LocalAddr().String())
			if err != nil {
				return err
			}

			proto := srcConn.LocalAddr().Network()
			proto = strings.ToUpper(proto)
			if strings.Contains(cHost, ":") {
				proto = fmt.Sprintf("%s6", proto)
			} else {
				proto = fmt.Sprintf("%s4", proto)
			}

			dstConn.Write([]byte(fmt.Sprintf("PROXY %s %s %s %s %s\r\n", proto, cHost, dHost, cPort, dPort)))
		}
	}

	if cAddr.connType == "unix" && lAddr.connType == "unix" {
		// Handle OOB if both src and dst are using unix sockets
		go unixRelay(srcConn, dstConn)
	} else {

		go genericRelay(srcConn, dstConn, false)
	}

	return nil
}

type lStruct struct {
	f          *os.File
	lConn      *net.Listener
	udpConn    *net.Conn
	lAddrIndex int
}

func (c *cmdForkproxy) Run(cmd *cobra.Command, args []string) error {
	// Only root should run this
	if os.Geteuid() != 0 {
		return fmt.Errorf("This must be run as root")
	}

	// Sanity checks
	if len(args) != 12 {
		cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	// Check where we are in initialization
	if C.whoami != C.FORKPROXY_PARENT && C.whoami != C.FORKPROXY_CHILD {
		return fmt.Errorf("Failed to call forkproxy constructor")
	}

	listenAddr := args[1]
	lAddr, err := parseAddr(listenAddr)
	if err != nil {
		return err
	}

	connectAddr := args[3]
	cAddr, err := parseAddr(connectAddr)
	if err != nil {
		return err
	}

	if (lAddr.connType == "udp" || lAddr.connType == "tcp") && cAddr.connType == "udp" || cAddr.connType == "tcp" {
		err := fmt.Errorf("Invalid port range")
		if len(lAddr.addr) > 1 && len(cAddr.addr) > 1 && (len(cAddr.addr) != len(lAddr.addr)) {
			fmt.Println(err)
			return err
		} else if len(lAddr.addr) == 1 && len(cAddr.addr) > 1 {
			fmt.Println(err)
			return err
		}
	}

	if C.whoami == C.FORKPROXY_CHILD {
		defer syscall.Close(forkproxyUDSSockFDNum)

		if lAddr.connType == "unix" && !lAddr.abstract {
			err := os.Remove(lAddr.addr[0])
			if err != nil && !os.IsNotExist(err) {
				return err
			}
		}

		for _, addr := range lAddr.addr {
			file, err := getListenerFile(lAddr.connType, addr)
			if err != nil {
				return err
			}

		sAgain:
			err = shared.AbstractUnixSendFd(forkproxyUDSSockFDNum, int(file.Fd()))
			if err != nil {
				errno, ok := shared.GetErrno(err)
				if ok && (errno == syscall.EAGAIN) {
					goto sAgain
				}
				break
			}
			file.Close()
		}

		if lAddr.connType == "unix" && !lAddr.abstract {
			var err error

			listenAddrGid := -1
			if args[6] != "" {
				listenAddrGid, err = strconv.Atoi(args[6])
				if err != nil {
					return err
				}
			}

			listenAddrUid := -1
			if args[7] != "" {
				listenAddrUid, err = strconv.Atoi(args[7])
				if err != nil {
					return err
				}
			}

			if listenAddrGid != -1 || listenAddrUid != -1 {
				err = os.Chown(lAddr.addr[0], listenAddrUid, listenAddrGid)
				if err != nil {
					return err
				}
			}

			var listenAddrMode os.FileMode
			if args[8] != "" {
				tmp, err := strconv.ParseUint(args[8], 8, 0)
				if err != nil {
					return err
				}

				listenAddrMode = os.FileMode(tmp)
				err = os.Chmod(lAddr.addr[0], listenAddrMode)
				if err != nil {
					return err
				}
			}
		}

		return err
	}

	files := []*os.File{}
	for range lAddr.addr {
	rAgain:
		f, err := shared.AbstractUnixReceiveFd(forkproxyUDSSockFDNum)
		if err != nil {
			errno, ok := shared.GetErrno(err)
			if ok && (errno == syscall.EAGAIN) {
				goto rAgain
			}

			fmt.Printf("Failed to receive fd from listener process: %v\n", err)
			syscall.Close(forkproxyUDSSockFDNum)
			return err
		}
		files = append(files, f)
	}
	syscall.Close(forkproxyUDSSockFDNum)

	var listenerMap map[int]*lStruct

	isUDPListener := lAddr.connType == "udp"
	listenerMap = make(map[int]*lStruct, len(lAddr.addr))
	if isUDPListener {
		for i, f := range files {
			listenerMap[int(f.Fd())] = &lStruct{
				f:          f,
				lAddrIndex: i,
			}
		}
	} else {
		for i, f := range files {
			listener, err := net.FileListener(f)
			if err != nil {
				fmt.Printf("Failed to re-assemble listener: %v", err)
				return err
			}
			listenerMap[int(f.Fd())] = &lStruct{
				lConn:      &listener,
				lAddrIndex: i,
			}
		}
	}

	// Drop privilege if requested
	gid := uint64(0)
	if args[9] != "" {
		gid, err = strconv.ParseUint(args[9], 10, 32)
		if err != nil {
			return err
		}
	}

	uid := uint64(0)
	if args[10] != "" {
		uid, err = strconv.ParseUint(args[10], 10, 32)
		if err != nil {
			return err
		}
	}

	if uid != 0 || gid != 0 {
		ret := C.switch_uid_gid(C.uint32_t(uid), C.uint32_t(gid))
		if ret < 0 {
			return fmt.Errorf("Failed to switch to uid %d and gid %d", uid, gid)
		}
	}

	// Handle SIGTERM which is sent when the proxy is to be removed
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)

	if lAddr.connType == "unix" && !lAddr.abstract {
		defer os.Remove(lAddr.addr[0])
	}

	epFd := C.epoll_create1(C.EPOLL_CLOEXEC)
	if epFd < 0 {
		return fmt.Errorf("Failed to create new epoll instance")
	}

	// Wait for SIGTERM and close the listener in order to exit the loop below
	self := syscall.Getpid()
	go func() {
		<-sigs

		for _, f := range files {
			C.epoll_ctl(epFd, C.EPOLL_CTL_DEL, C.int(f.Fd()), nil)
			f.Close()
		}
		syscall.Close(int(epFd))

		if !isUDPListener {
			for _, l := range listenerMap {
				conn := (*l).lConn
				(*conn).Close()
			}
		}

		syscall.Kill(self, syscall.SIGKILL)
	}()
	defer syscall.Kill(self, syscall.SIGTERM)

	for _, f := range files {
		var ev C.struct_epoll_event
		ev.events = C.EPOLLIN
		if isUDPListener {
			ev.events |= C.EPOLLONESHOT
		}

		*(*C.int)(unsafe.Pointer(uintptr(unsafe.Pointer(&ev)) + unsafe.Sizeof(ev.events))) = C.int(f.Fd())
		ret := C.epoll_ctl(epFd, C.EPOLL_CTL_ADD, C.int(f.Fd()), &ev)
		if ret < 0 {
			return fmt.Errorf("Failed to add listener fd to epoll instance")
		}
	}

	for {
		var events [10]C.struct_epoll_event

		nfds := C.epoll_wait(epFd, &events[0], 10, -1)
		if nfds < 0 {
			fmt.Printf("Failed to wait on epoll instance")
			break
		}

		for i := C.int(0); i < nfds; i++ {
			curFd := *(*C.int)(unsafe.Pointer(uintptr(unsafe.Pointer(&events[i])) + unsafe.Sizeof(events[i].events)))
			srcConn, ok := listenerMap[int(curFd)]
			if !ok {
				continue
			}

			err := listenerInstance(epFd, lAddr, cAddr, curFd, srcConn, args[11] == "true")
			if err != nil {
				fmt.Printf("Failed to prepare new listener instance: %s", err)
			}
		}
	}

	fmt.Printf("Stopping proxy\n")
	return nil
}

func proxyCopy(dst net.Conn, src net.Conn) error {
	var err error

	// Attempt casting to UDP connections
	srcUdp, srcIsUdp := src.(*net.UDPConn)
	dstUdp, dstIsUdp := dst.(*net.UDPConn)

	buf := make([]byte, 32*1024)
	for {
	rAgain:
		var nr int
		var er error

		if srcIsUdp && srcUdp.RemoteAddr() == nil {
			var addr net.Addr
			nr, addr, er = srcUdp.ReadFrom(buf)
			if er == nil {
				// Look for existing UDP session
				udpSessionsLock.Lock()
				us, ok := udpSessions[addr.String()]
				udpSessionsLock.Unlock()

				if !ok {
					dc, err := net.Dial(dst.RemoteAddr().Network(), dst.RemoteAddr().String())
					if err != nil {
						return err
					}

					us = &udpSession{
						client: addr,
						target: dc,
					}

					udpSessionsLock.Lock()
					udpSessions[addr.String()] = us
					udpSessionsLock.Unlock()

					go proxyCopy(src, dc)
					us.timer = time.AfterFunc(30*time.Minute, func() {
						us.target.Close()

						udpSessionsLock.Lock()
						delete(udpSessions, addr.String())
						udpSessionsLock.Unlock()
					})
				}

				us.timer.Reset(30 * time.Minute)
				dst = us.target
				dstUdp, dstIsUdp = dst.(*net.UDPConn)
			}
		} else {
			nr, er = src.Read(buf)
		}

		// keep retrying on EAGAIN
		errno, ok := shared.GetErrno(er)
		if ok && (errno == syscall.EAGAIN) {
			goto rAgain
		}

		if nr > 0 {
		wAgain:
			var nw int
			var ew error

			if dstIsUdp && dstUdp.RemoteAddr() == nil {
				var us *udpSession

				udpSessionsLock.Lock()
				for _, v := range udpSessions {
					if v.target.LocalAddr() == src.LocalAddr() {
						us = v
						break
					}
				}
				udpSessionsLock.Unlock()

				if us == nil {
					return fmt.Errorf("Connection expired")
				}

				us.timer.Reset(30 * time.Minute)

				nw, ew = dstUdp.WriteTo(buf[0:nr], us.client)
			} else {
				nw, ew = dst.Write(buf[0:nr])
			}

			// keep retrying on EAGAIN
			errno, ok := shared.GetErrno(ew)
			if ok && (errno == syscall.EAGAIN) {
				goto wAgain

			}
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}

			break
		}
	}

	return err
}

func genericRelay(dst net.Conn, src net.Conn, timeout bool) {
	relayer := func(src net.Conn, dst net.Conn, ch chan error) {
		ch <- proxyCopy(src, dst)
	}

	chSend := make(chan error)
	chRecv := make(chan error)

	go relayer(src, dst, chRecv)

	_, ok := dst.(*net.UDPConn)
	if !ok {
		go relayer(dst, src, chSend)
	}

	select {
	case errSnd := <-chSend:
		if errSnd != nil {
			fmt.Printf("Error while sending data: %v\n", errSnd)
		}

	case errRcv := <-chRecv:
		if errRcv != nil {
			fmt.Printf("Error while reading data: %v\n", errRcv)
		}
	}

	src.Close()
	dst.Close()
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

	if !shared.StringInSlice(fields[0], []string{"tcp", "udp", "unix"}) {
		return nil, fmt.Errorf("Unknown connection type '%s'", fields[0])
	}

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
	address, port, err := net.SplitHostPort(fields[1])
	if err != nil {
		return nil, err
	}

	// Split <ports> into individual ports and port ranges
	ports := strings.SplitN(port, ",", -1)
	for _, p := range ports {
		portFirst, portRange, err := parsePortRange(p)
		if err != nil {
			return nil, err
		}

		for i := int64(0); i < portRange; i++ {
			var newAddr string
			if strings.Contains(address, ":") {
				// IPv6 addresses need to be enclosed in square brackets
				newAddr = fmt.Sprintf("[%s]:%d", address, portFirst+i)
			} else {
				newAddr = fmt.Sprintf("%s:%d", address, portFirst+i)
			}
			newProxyAddr.addr = append(newProxyAddr.addr, newAddr)
		}
	}

	return newProxyAddr, nil
}
