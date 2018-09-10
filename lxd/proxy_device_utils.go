package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

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

func setupProxyProcInfo(c container, device map[string]string) (*proxyProcInfo, error) {
	pid := c.InitPID()
	containerPid := strconv.Itoa(int(pid))
	lxdPid := strconv.Itoa(os.Getpid())

	connectAddr := device["connect"]
	listenAddr := device["listen"]

	connectionFields := strings.SplitN(connectAddr, ":", 2)
	listenerFields := strings.SplitN(listenAddr, ":", 2)

	if !shared.StringInSlice(connectionFields[0], []string{"tcp", "udp", "unix"}) {
		return nil, fmt.Errorf("Proxy device doesn't support the connection type: %s", connectionFields[0])
	}

	if !shared.StringInSlice(listenerFields[0], []string{"tcp", "udp", "unix"}) {
		return nil, fmt.Errorf("Proxy device doesn't support the listener type: %s", listenerFields[0])
	}

	listenPid := "-1"
	connectPid := "-1"

	bindVal, exists := device["bind"]

	if bindVal == "host" || !exists {
		listenPid = lxdPid
		connectPid = containerPid
	} else if bindVal == "container" {
		listenPid = containerPid
		connectPid = lxdPid
	} else {
		return nil, fmt.Errorf("Invalid binding side given. Must be \"host\" or \"container\"")
	}

	if connectionFields[0] == "unix" && !strings.HasPrefix(connectionFields[1], "@") && bindVal == "container" {
		connectAddr = fmt.Sprintf("%s:%s", connectionFields[0], shared.HostPath(connectionFields[1]))
	}

	if listenerFields[0] == "unix" && !strings.HasPrefix(connectionFields[1], "@") && bindVal == "host" {
		listenAddr = fmt.Sprintf("%s:%s", listenerFields[0], shared.HostPath(listenerFields[1]))
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

	// Check if it's a proxy process
	cmdPath, err := os.Readlink(fmt.Sprintf("/proc/%s/exe", pidString))
	if err != nil {
		cmdPath = ""
	}

	// Deal with deleted paths
	cmdName := filepath.Base(strings.Split(cmdPath, " ")[0])
	if cmdName != "lxd" {
		os.Remove(pidPath)
		return nil
	}

	// Parse the pid
	pidInt, err := strconv.Atoi(pidString)
	if err != nil {
		return err
	}

	err = syscall.Kill(pidInt, syscall.SIGTERM)
	if err != nil {
		return err
	}

	go func() {
		for i := 0; i < 6; i++ {
			time.Sleep(500 * time.Millisecond)
			// Check if the process still exists
			if !shared.PathExists(fmt.Sprintf("/proc/%s", pidString)) {
				return
			}

			// Check if it's a proxy process
			cmdPath, err := os.Readlink(fmt.Sprintf("/proc/%s/exe", pidString))
			if err != nil {
				cmdPath = ""
			}

			// Deal with deleted paths
			cmdName := filepath.Base(strings.Split(cmdPath, " ")[0])
			if cmdName != "lxd" {
				return
			}
		}
		syscall.Kill(pidInt, syscall.SIGKILL)
	}()

	// Cleanup
	os.Remove(pidPath)
	return nil
}
