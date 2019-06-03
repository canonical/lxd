package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared"
)

type proxyProcInfo struct {
	listenPid      string
	connectPid     string
	connectAddr    string
	listenAddr     string
	listenAddrGid  string
	listenAddrUid  string
	listenAddrMode string
	securityUid    string
	securityGid    string
	proxyProtocol  string
}

func parseAddr(addr string) (string, string) {
	fields := strings.SplitN(addr, ":", 2)
	return fields[0], fields[1]
}

func rewriteHostAddr(addr string) string {
	proto, addr := parseAddr(addr)
	if proto == "unix" && !strings.HasPrefix(addr, "@") {
		// Unix non-abstract sockets need to be addressed to the host
		// filesystem, not be scoped inside the LXD snap.
		addr = shared.HostPath(addr)
	}
	return fmt.Sprintf("%s:%s", proto, addr)
}

func setupProxyProcInfo(c container, device map[string]string) (*proxyProcInfo, error) {
	pid := c.InitPID()
	containerPid := strconv.Itoa(int(pid))
	lxdPid := strconv.Itoa(os.Getpid())

	connectAddr := device["connect"]
	listenAddr := device["listen"]

	if proto, _ := parseAddr(connectAddr); !shared.StringInSlice(proto, []string{"tcp", "udp", "unix"}) {
		return nil, fmt.Errorf("Proxy device doesn't support the connection type: %s", proto)
	}
	if proto, _ := parseAddr(listenAddr); !shared.StringInSlice(proto, []string{"tcp", "udp", "unix"}) {
		return nil, fmt.Errorf("Proxy device doesn't support the listener type: %s", proto)
	}

	var listenPid string
	var connectPid string

	bindVal, exists := device["bind"]
	if !exists {
		bindVal = "host"
	}

	switch bindVal {
	case "host":
		listenPid = lxdPid
		connectPid = containerPid
		listenAddr = rewriteHostAddr(listenAddr)
	case "container":
		listenPid = containerPid
		connectPid = lxdPid
		connectAddr = rewriteHostAddr(connectAddr)
	default:
		return nil, fmt.Errorf("Invalid binding side given. Must be \"host\" or \"container\"")
	}

	p := &proxyProcInfo{
		listenPid:      listenPid,
		connectPid:     connectPid,
		connectAddr:    connectAddr,
		listenAddr:     listenAddr,
		listenAddrGid:  device["gid"],
		listenAddrUid:  device["uid"],
		listenAddrMode: device["mode"],
		securityGid:    device["security.gid"],
		securityUid:    device["security.uid"],
		proxyProtocol:  device["proxy_protocol"],
	}

	return p, nil
}

func killProxyProc(pidPath string) error {
	// Get the contents of the pid file
	contents, err := ioutil.ReadFile(pidPath)
	if err != nil {
		return err
	}
	pidString := strings.TrimSpace(string(contents))

	// Check if the process still exists
	if !shared.PathExists(fmt.Sprintf("/proc/%s", pidString)) {
		os.Remove(pidPath)
		return nil
	}

	// Check if it's forkdns
	cmdArgs, err := ioutil.ReadFile(fmt.Sprintf("/proc/%s/cmdline", pidString))
	if err != nil {
		os.Remove(pidPath)
		return nil
	}

	cmdFields := strings.Split(string(bytes.TrimRight(cmdArgs, string("\x00"))), string(byte(0)))
	if len(cmdFields) < 5 || cmdFields[1] != "forkproxy" {
		os.Remove(pidPath)
		return nil
	}

	// Parse the pid
	pidInt, err := strconv.Atoi(pidString)
	if err != nil {
		return err
	}

	// Actually kill the process
	err = unix.Kill(pidInt, unix.SIGKILL)
	if err != nil {
		return err
	}

	// Cleanup
	os.Remove(pidPath)
	return nil
}
